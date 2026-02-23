package slack

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	slackapi "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/achetronic/magec/server/clients/msgutil"
	"github.com/achetronic/magec/server/store"
)

const (
	ResponseModeText   = "text"
	ResponseModeVoice  = "voice"
	ResponseModeMirror = "mirror"
	ResponseModeBoth   = "both"

	progressTimeout = 30 * time.Second
)

type AgentInfo struct {
	ID   string
	Name string
}

type Client struct {
	clientDef store.ClientDefinition
	agentURL  string
	agents    []AgentInfo
	logger    *slog.Logger

	api    *slackapi.Client
	socket *socketmode.Client
	cancel context.CancelFunc

	activeAgentMu sync.RWMutex
	activeAgent   map[string]string // channelID -> agentID

	responseMu           sync.RWMutex
	responseModeOverride string

	botUserID string
}

func New(clientDef store.ClientDefinition, agentURL string, agents []AgentInfo, logger *slog.Logger) (*Client, error) {
	if clientDef.Config.Slack == nil {
		return nil, fmt.Errorf("slack config is required")
	}
	cfg := clientDef.Config.Slack
	if cfg.BotToken == "" {
		return nil, fmt.Errorf("slack bot token is required")
	}
	if cfg.AppToken == "" {
		return nil, fmt.Errorf("slack app token is required")
	}

	api := slackapi.New(
		cfg.BotToken,
		slackapi.OptionAppLevelToken(cfg.AppToken),
	)

	socket := socketmode.New(api)

	return &Client{
		api:         api,
		socket:      socket,
		clientDef:   clientDef,
		agentURL:    agentURL,
		agents:      agents,
		activeAgent: make(map[string]string),
		logger:      logger,
	}, nil
}

func (c *Client) Start(ctx context.Context) error {
	authResp, err := c.api.AuthTestContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to authenticate slack bot: %w", err)
	}
	c.botUserID = authResp.UserID
	c.logger.Info("Slack bot started", "bot_user_id", c.botUserID, "team", authResp.Team)

	smCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go func() {
		for evt := range c.socket.Events {
			switch evt.Type {
			case socketmode.EventTypeEventsAPI:
				c.handleEventsAPI(evt)
			case socketmode.EventTypeConnected:
				c.logger.Info("Slack Socket Mode connected")
			case socketmode.EventTypeConnectionError:
				c.logger.Error("Slack Socket Mode connection error", "data", evt.Data)
			}
		}
	}()

	if err := c.socket.RunContext(smCtx); err != nil {
		if smCtx.Err() != nil {
			return nil
		}
		return fmt.Errorf("slack socket mode error: %w", err)
	}
	return nil
}

func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.logger.Info("Slack bot stopped")
}

func (c *Client) handleEventsAPI(evt socketmode.Event) {
	eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		c.socket.Ack(*evt.Request)
		return
	}
	c.socket.Ack(*evt.Request)

	switch eventsAPIEvent.InnerEvent.Type {
	case string(slackevents.AppMention):
		c.handleAppMention(eventsAPIEvent)
	case string(slackevents.Message):
		c.handleMessage(eventsAPIEvent)
	}
}

func (c *Client) handleAppMention(event slackevents.EventsAPIEvent) {
	ev, ok := event.InnerEvent.Data.(*slackevents.AppMentionEvent)
	if !ok || ev == nil {
		return
	}
	if ev.User == c.botUserID {
		return
	}
	if !c.isAllowed(ev.User, ev.Channel) {
		c.logger.Debug("Unauthorized Slack mention", "user", ev.User, "channel", ev.Channel)
		return
	}

	text := c.stripBotMention(ev.Text)
	if text == "" {
		return
	}

	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp
	}

	if c.handleBotCommand(ev.User, ev.Channel, text, threadTS) {
		return
	}

	c.logger.Info("Slack mention received", "user", ev.User, "channel", ev.Channel, "text", text)
	c.processMessage(ev.User, ev.Channel, "channel", text, threadTS, ev.TimeStamp, event.TeamID, false)
}

func (c *Client) handleMessage(event slackevents.EventsAPIEvent) {
	ev, ok := event.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok || ev == nil {
		return
	}
	if ev.User == c.botUserID || ev.User == "" {
		return
	}
	if ev.BotID != "" {
		return
	}
	if ev.ChannelType != "im" {
		return
	}
	if !c.isAllowed(ev.User, ev.Channel) {
		c.logger.Debug("Unauthorized Slack DM", "user", ev.User, "channel", ev.Channel)
		return
	}

	if c.handleAudioClip(ev, event.TeamID) {
		return
	}

	text := strings.TrimSpace(ev.Text)
	if text == "" {
		return
	}

	threadTS := ev.ThreadTimeStamp

	if c.handleBotCommand(ev.User, ev.Channel, text, threadTS) {
		return
	}

	c.logger.Info("Slack DM received", "user", ev.User, "channel", ev.Channel, "text", text)
	c.processMessage(ev.User, ev.Channel, "im", text, threadTS, ev.TimeStamp, event.TeamID, false)
}

func (c *Client) handleAudioClip(ev *slackevents.MessageEvent, teamID string) bool {
	if ev.Message == nil || len(ev.Message.Files) == 0 {
		return false
	}

	for _, file := range ev.Message.Files {
		if !strings.HasPrefix(file.Mimetype, "audio/") {
			continue
		}

		c.logger.Info("Slack audio clip received",
			"user", ev.User,
			"channel", ev.Channel,
			"filetype", file.Filetype,
			"mimetype", file.Mimetype,
			"size", file.Size,
			"fileID", file.ID,
			"urlPrivate", file.URLPrivate,
			"urlPrivateDownload", file.URLPrivateDownload,
		)

		downloadURL := file.URLPrivateDownload
		if downloadURL == "" {
			downloadURL = file.URLPrivate
		}

		fullFile, _, _, err := c.api.GetFileInfo(file.ID, 0, 0)
		if err != nil {
			c.logger.Warn("Failed to get file info from Slack API, using event URLs", "error", err, "fileID", file.ID)
		} else {
			c.logger.Info("File info from API",
				"fileID", fullFile.ID,
				"urlPrivate", fullFile.URLPrivate,
				"urlPrivateDownload", fullFile.URLPrivateDownload,
			)
			if fullFile.URLPrivateDownload != "" {
				downloadURL = fullFile.URLPrivateDownload
			} else if fullFile.URLPrivate != "" {
				downloadURL = fullFile.URLPrivate
			}
		}

		if downloadURL == "" {
			c.logger.Error("Audio clip has no download URL")
			c.postMessage(ev.Channel, "Sorry, I couldn't download your audio clip.", "")
			return true
		}

		audioData, err := c.downloadSlackFile(downloadURL)
		if err != nil {
			c.logger.Error("Failed to download audio clip", "error", err)
			c.postMessage(ev.Channel, "Sorry, I couldn't download your audio clip.", "")
			return true
		}

		agentID := c.getActiveAgentID(ev.Channel)
		wavData, err := c.convertAudioToWav(audioData)
		if err != nil {
			c.logger.Error("Failed to convert audio clip", "error", err)
			c.postMessage(ev.Channel, "Sorry, I couldn't process your audio clip.", "")
			return true
		}

		text, err := c.transcribeAudio(wavData, agentID)
		if err != nil {
			c.logger.Error("Failed to transcribe audio clip", "error", err)
			c.postMessage(ev.Channel, "Sorry, I couldn't transcribe your audio clip.", "")
			return true
		}

		c.logger.Info("Transcribed audio clip", "text", text)

		c.processMessage(ev.User, ev.Channel, "im", text, ev.ThreadTimeStamp, ev.TimeStamp, teamID, true)
		return true
	}
	return false
}

func (c *Client) handleBotCommand(userID, channelID, text, threadTS string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))

	if !strings.HasPrefix(lower, "!") {
		return false
	}
	lower = lower[1:]
	text = strings.TrimSpace(text[1:])

	if lower == "help" {
		helpText := "*Available commands:*\n" +
			"• `!help` — Show this help message\n" +
			"• `!agent` — Show or switch the active agent\n" +
			"• `!agent <id>` — Switch to a specific agent\n" +
			"• `!reset` — Reset the conversation session\n" +
			"• `!responsemode` — Show or change the response mode\n" +
			"• `!responsemode <mode>` — Set response mode (`text`, `voice`, `mirror`, `both`, `reset`)"
		c.postMessage(channelID, helpText, threadTS)
		return true
	}

	if lower == "agent" {
		currentID := c.getActiveAgentID(channelID)
		current := c.getAgentInfo(currentID)
		currentLabel := currentID
		if current != nil && current.Name != "" {
			currentLabel = fmt.Sprintf("%s (`%s`)", current.Name, currentID)
		}
		var agentList string
		for _, a := range c.agents {
			marker := "  "
			if a.ID == currentID {
				marker = "▸ "
			}
			label := a.ID
			if a.Name != "" {
				label = fmt.Sprintf("%s (`%s`)", a.Name, a.ID)
			}
			agentList += fmt.Sprintf("%s%s\n", marker, label)
		}
		msg := fmt.Sprintf("*Active agent:* %s\n\n*Available agents:*\n%s\nUsage: `!agent <id>`", currentLabel, agentList)
		c.postMessage(channelID, msg, threadTS)
		return true
	}

	if strings.HasPrefix(lower, "agent ") {
		agentID := strings.TrimSpace(text[6:])
		found := false
		for _, a := range c.agents {
			if a.ID == agentID {
				found = true
				break
			}
		}
		if !found {
			var ids []string
			for _, a := range c.agents {
				ids = append(ids, "`"+a.ID+"`")
			}
			c.postMessage(channelID, fmt.Sprintf("Unknown agent `%s`. Available: %s", agentID, strings.Join(ids, ", ")), threadTS)
			return true
		}
		c.setActiveAgentID(channelID, agentID)
		agent := c.getAgentInfo(agentID)
		label := agentID
		if agent != nil && agent.Name != "" {
			label = agent.Name
		}
		c.logger.Info("Slack agent switched", "channel", channelID, "agent", agentID)
		c.postMessage(channelID, fmt.Sprintf("Switched to agent *%s* (`%s`)", label, agentID), threadTS)
		return true
	}

	if lower == "responsemode" {
		current := c.getResponseMode()
		c.responseMu.RLock()
		overridden := c.responseModeOverride != ""
		c.responseMu.RUnlock()

		status := fmt.Sprintf("*Response mode:* `%s`", current)
		if overridden {
			status += fmt.Sprintf(" (override, config: `%s`)", c.clientDef.Config.Slack.ResponseMode)
		}
		status += "\n*Options:* `text`, `voice`, `mirror`, `both`, `reset`"

		c.postMessage(channelID, status, threadTS)
		return true
	}

	if lower == "reset" {
		agentID := c.getActiveAgentID(channelID)
		sessionID := c.buildSessionID(channelID, "")
		if err := c.deleteSession(agentID, sessionID); err != nil {
			c.logger.Error("Failed to delete session", "error", err)
			c.postMessage(channelID, "Failed to reset session.", threadTS)
			return true
		}
		c.logger.Info("Session reset", "channel", channelID, "agent", agentID, "session", sessionID)
		agent := c.getAgentInfo(agentID)
		label := agentID
		if agent != nil && agent.Name != "" {
			label = agent.Name
		}
		c.postMessage(channelID, fmt.Sprintf("Session reset for *%s*. Next message starts a fresh conversation.", label), threadTS)
		return true
	}

	if strings.HasPrefix(lower, "responsemode ") {
		arg := strings.TrimSpace(text[13:])
		return c.handleResponseModeCommand(channelID, arg, threadTS)
	}

	return false
}

func (c *Client) handleResponseModeCommand(channelID, arg, threadTS string) bool {
	validModes := []string{ResponseModeText, ResponseModeVoice, ResponseModeMirror, ResponseModeBoth}

	if arg == "reset" {
		c.responseMu.Lock()
		c.responseModeOverride = ""
		c.responseMu.Unlock()
		c.logger.Info("Response mode override cleared",
			"config_mode", c.clientDef.Config.Slack.ResponseMode,
		)
		c.postMessage(channelID, fmt.Sprintf("Response mode reset to config default: `%s`", c.clientDef.Config.Slack.ResponseMode), threadTS)
		return true
	}

	if !slices.Contains(validModes, arg) {
		c.postMessage(channelID, fmt.Sprintf("Invalid mode `%s`. Valid options: `text`, `voice`, `mirror`, `both`, `reset`", arg), threadTS)
		return true
	}

	c.responseMu.Lock()
	c.responseModeOverride = arg
	c.responseMu.Unlock()

	c.logger.Info("Response mode overridden", "new_mode", arg)
	c.postMessage(channelID, fmt.Sprintf("Response mode set to `%s` (until restart)", arg), threadTS)
	return true
}

func (c *Client) getResponseMode() string {
	c.responseMu.RLock()
	defer c.responseMu.RUnlock()
	if c.responseModeOverride != "" {
		return c.responseModeOverride
	}
	mode := c.clientDef.Config.Slack.ResponseMode
	if mode == "" {
		return ResponseModeText
	}
	return mode
}

func (c *Client) buildSessionID(channelID, threadTS string) string {
	agentID := c.getActiveAgentID(channelID)
	if threadTS != "" {
		return fmt.Sprintf("slack_%s_%s_%s", channelID, threadTS, agentID)
	}
	return fmt.Sprintf("slack_%s_%s", channelID, agentID)
}

func (c *Client) deleteSession(agentID, sessionID string) error {
	url := fmt.Sprintf("%s/apps/%s/users/%s/sessions/%s", c.agentURL, agentID, "default_user", sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}
	c.setAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("failed to delete session: status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) processMessage(userID, channelID, channelType, text, threadTS, messageTS, teamID string, inputWasVoice bool) {
	msgRef := slackapi.NewRefToMessage(channelID, messageTS)
	c.addReaction("eyes", msgRef)

	agentID := c.getActiveAgentID(channelID)
	sessionID := c.buildSessionID(channelID, threadTS)

	if err := c.ensureSession(agentID, "default_user", sessionID); err != nil {
		c.logger.Warn("Failed to ensure session, continuing anyway", "error", err)
	}

	artifactsBefore := c.listArtifacts(agentID, "default_user", sessionID)

	validatedText, truncated := msgutil.ValidateInputLength(text, msgutil.DefaultMaxInputLength)
	if truncated {
		c.logger.Warn("Inbound message truncated",
			"channel", channelID,
			"original_len", len([]rune(text)),
		)
	}

	meta := c.buildMessageContext(userID, channelID, channelType, threadTS, teamID)
	fullMessage := meta + validatedText

	reqBody := map[string]interface{}{
		"appName":   agentID,
		"userId":    "default_user",
		"sessionId": sessionID,
		"newMessage": map[string]interface{}{
			"role": "user",
			"parts": []map[string]string{
				{"text": fullMessage},
			},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		c.logger.Error("Failed to marshal request", "error", err)
		c.setReaction("x", msgRef)
		return
	}

	c.setReaction("brain", msgRef)

	progressTimer := time.AfterFunc(progressTimeout, func() {
		c.postMessage(channelID, "Still working on it, this may take a moment...", threadTS)
	})
	defer progressTimer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", c.agentURL+"/run", bytes.NewReader(jsonBody))
	if err != nil {
		c.logger.Error("Failed to create request", "error", err)
		c.setReaction("x", msgRef)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.logger.Error("Failed to call agent", "error", err)
		c.setReaction("x", msgRef)
		c.postMessage(channelID, fmt.Sprintf("Failed to reach the agent: %s", sanitizeError(err)), threadTS)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.logger.Error("Agent returned error", "status", resp.StatusCode, "body", string(body))
		c.setReaction("x", msgRef)
		c.postMessage(channelID, fmt.Sprintf("Agent returned an error (status %d). Please try again.", resp.StatusCode), threadTS)
		return
	}

	var events []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		c.logger.Error("Failed to decode response", "error", err)
		c.setReaction("x", msgRef)
		c.postMessage(channelID, "Failed to parse the agent response. Please try again.", threadTS)
		return
	}

	progressTimer.Stop()
	c.setReaction("white_check_mark", msgRef)

	responseText := c.extractResponseText(events)
	c.sendResponse(channelID, responseText, threadTS, inputWasVoice)
	c.sendNewArtifacts(channelID, threadTS, agentID, "default_user", sessionID, artifactsBefore)
}

func (c *Client) sendResponse(channelID, text, threadTS string, inputWasVoice bool) {
	mode := c.getResponseMode()

	sendText := false
	sendVoice := false

	switch mode {
	case ResponseModeVoice:
		sendVoice = true
	case ResponseModeMirror:
		if inputWasVoice {
			sendVoice = true
		} else {
			sendText = true
		}
	case ResponseModeBoth:
		sendText = true
		sendVoice = true
	default:
		sendText = true
	}

	if sendText {
		c.postMessage(channelID, text, threadTS)
	}

	if sendVoice {
		agentID := c.getActiveAgentID(channelID)
		c.sendVoiceResponse(channelID, text, threadTS, agentID)
	}
}

func (c *Client) sendVoiceResponse(channelID, text, threadTS, agentID string) {
	audioData, err := c.generateTTS(text, agentID)
	if err != nil {
		c.logger.Error("Failed to generate TTS", "error", err)
		c.postMessage(channelID, text, threadTS)
		return
	}

	params := slackapi.UploadFileV2Parameters{
		Channel:        channelID,
		Filename:       "voice.ogg",
		FileSize:       len(audioData),
		Reader:         bytes.NewReader(audioData),
		Title:          "Voice response",
		AltTxt:         text,
		InitialComment: "",
	}
	if threadTS != "" {
		params.ThreadTimestamp = threadTS
	}

	_, err = c.api.UploadFileV2(params)
	if err != nil {
		c.logger.Error("Failed to upload voice file", "channel", channelID, "error", err)
		c.postMessage(channelID, text, threadTS)
	}
}

func (c *Client) generateTTS(text string, agentID string) ([]byte, error) {
	reqBody := map[string]interface{}{
		"input":           text,
		"response_format": "opus",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	ttsURL := strings.TrimSuffix(c.agentURL, "/agent") + "/voice/" + agentID + "/speech"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", ttsURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("TTS failed with status %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

func (c *Client) downloadSlackFile(fileURL string) ([]byte, error) {
	token := c.clientDef.Config.Slack.BotToken

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		},
	}

	req, err := http.NewRequest(http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed with status %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read file data: %w", err)
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		return nil, fmt.Errorf("download returned HTML instead of file data (Content-Type: %s, size: %d)", ct, len(data))
	}

	c.logger.Info("Downloaded slack file", "url", fileURL, "size", len(data), "contentType", ct)
	return data, nil
}

func (c *Client) convertAudioToWav(audioData []byte) ([]byte, error) {
	tmpIn, err := os.CreateTemp("", "magec-audio-in-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp input file: %w", err)
	}
	defer os.Remove(tmpIn.Name())

	if _, err := tmpIn.Write(audioData); err != nil {
		tmpIn.Close()
		return nil, fmt.Errorf("failed to write temp input file: %w", err)
	}
	tmpIn.Close()

	cmd := exec.Command("ffmpeg",
		"-i", tmpIn.Name(),
		"-ar", "16000",
		"-ac", "1",
		"-f", "wav",
		"pipe:1",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg conversion failed: %w, stderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

func (c *Client) transcribeAudio(wavData []byte, agentID string) (string, error) {
	var buf bytes.Buffer
	boundary := "----WebKitFormBoundary7MA4YWxkTrZu0gW"

	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Disposition: form-data; name=\"file\"; filename=\"audio.wav\"\r\n")
	buf.WriteString("Content-Type: audio/wav\r\n\r\n")
	buf.Write(wavData)
	buf.WriteString(fmt.Sprintf("\r\n--%s--\r\n", boundary))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	transcriptionURL := strings.TrimSuffix(c.agentURL, "/agent") + "/voice/" + agentID + "/transcription"

	req, err := http.NewRequestWithContext(ctx, "POST", transcriptionURL, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", fmt.Sprintf("multipart/form-data; boundary=%s", boundary))
	c.setAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("transcription failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Text, nil
}

func (c *Client) buildMessageContext(userID, channelID, channelType, threadTS, teamID string) string {
	meta := map[string]interface{}{
		"source":           "slack",
		"slack_user_id":    userID,
		"slack_channel_id": channelID,
	}

	if channelType != "" {
		meta["slack_channel_type"] = channelType
	}
	if teamID != "" {
		meta["slack_team_id"] = teamID
	}
	if threadTS != "" {
		meta["slack_thread_ts"] = threadTS
	}

	userInfo, err := c.api.GetUserInfo(userID)
	if err == nil && userInfo != nil {
		if userInfo.Name != "" {
			meta["slack_username"] = userInfo.Name
		}
		if userInfo.RealName != "" {
			meta["slack_name"] = userInfo.RealName
		}
		if userInfo.Profile.Email != "" {
			meta["slack_email"] = userInfo.Profile.Email
		}
	}

	jsonBytes, err := json.Marshal(meta)
	if err != nil {
		c.logger.Warn("Failed to marshal message context metadata", "error", err)
		return ""
	}
	return fmt.Sprintf("<!--MAGEC_META:%s:MAGEC_META-->\n", string(jsonBytes))
}

func (c *Client) postMessage(channelID, text, threadTS string) {
	chunks := msgutil.SplitMessage(text, msgutil.SlackMaxMessageLength)
	for _, chunk := range chunks {
		opts := []slackapi.MsgOption{
			slackapi.MsgOptionText(chunk, false),
		}
		if threadTS != "" {
			opts = append(opts, slackapi.MsgOptionTS(threadTS))
		}
		_, _, err := c.api.PostMessage(channelID, opts...)
		if err != nil {
			c.logger.Error("Failed to send Slack message", "channel", channelID, "error", err)
			break
		}
	}
}

func (c *Client) addReaction(emoji string, ref slackapi.ItemRef) {
	if err := c.api.AddReaction(emoji, ref); err != nil {
		c.logger.Debug("Failed to add reaction", "emoji", emoji, "error", err)
	}
}

func (c *Client) setReaction(emoji string, ref slackapi.ItemRef) {
	c.addReaction(emoji, ref)
}

func sanitizeError(err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	for _, secret := range []string{"Bearer ", "xoxb-", "xapp-", "bot", "token"} {
		if strings.Contains(strings.ToLower(msg), strings.ToLower(secret)) {
			return "an internal error occurred"
		}
	}
	return msg
}

func (c *Client) listArtifacts(agentID, userID, sessionID string) []string {
	url := fmt.Sprintf("%s/apps/%s/users/%s/sessions/%s/artifacts", c.agentURL, agentID, userID, sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		c.logger.Debug("Failed to create artifact list request", "error", err)
		return nil
	}
	c.setAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.logger.Debug("Failed to list artifacts", "error", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var result []string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.logger.Debug("Failed to decode artifact list", "error", err)
		return nil
	}
	return result
}

func (c *Client) downloadArtifact(agentID, userID, sessionID, name string) ([]byte, string, error) {
	url := fmt.Sprintf("%s/apps/%s/users/%s/sessions/%s/artifacts/%s", c.agentURL, agentID, userID, sessionID, name)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	c.setAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("artifact download returned status %d", resp.StatusCode)
	}

	var artifact struct {
		Text       string `json:"text,omitempty"`
		InlineData *struct {
			MIMEType string `json:"mimeType"`
			Data     string `json:"data"`
		} `json:"inlineData,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&artifact); err != nil {
		return nil, "", fmt.Errorf("failed to decode artifact: %w", err)
	}

	if artifact.InlineData != nil {
		data, err := base64.StdEncoding.DecodeString(artifact.InlineData.Data)
		if err != nil {
			return nil, "", fmt.Errorf("failed to decode artifact binary data: %w", err)
		}
		return data, artifact.InlineData.MIMEType, nil
	}

	return []byte(artifact.Text), "text/plain", nil
}

func (c *Client) sendNewArtifacts(channelID, threadTS, agentID, userID, sessionID string, before []string) {
	after := c.listArtifacts(agentID, userID, sessionID)
	if len(after) == 0 {
		return
	}

	beforeSet := make(map[string]bool, len(before))
	for _, name := range before {
		beforeSet[name] = true
	}

	for _, name := range after {
		if beforeSet[name] {
			continue
		}

		data, _, err := c.downloadArtifact(agentID, userID, sessionID, name)
		if err != nil {
			c.logger.Error("Failed to download artifact", "name", name, "error", err)
			continue
		}

		params := slackapi.UploadFileV2Parameters{
			Channel:  channelID,
			Filename: name,
			FileSize: len(data),
			Reader:   bytes.NewReader(data),
			Title:    name,
		}
		if threadTS != "" {
			params.ThreadTimestamp = threadTS
		}

		_, err = c.api.UploadFileV2(params)
		if err != nil {
			c.logger.Error("Failed to upload artifact", "name", name, "error", err)
		}
	}
}

func (c *Client) setAuthHeader(req *http.Request) {
	if c.clientDef.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.clientDef.Token)
	}
}

func (c *Client) ensureSession(agentID, userID, sessionID string) error {
	url := fmt.Sprintf("%s/apps/%s/users/%s/sessions/%s", c.agentURL, agentID, userID, sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("failed to create session: status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) extractResponseText(events []map[string]interface{}) string {
	var result string
	for _, event := range events {
		content, ok := event["content"].(map[string]interface{})
		if !ok {
			continue
		}
		parts, ok := content["parts"].([]interface{})
		if !ok {
			continue
		}
		for _, part := range parts {
			partMap, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			if text, ok := partMap["text"].(string); ok {
				result += text
			}
		}
	}
	if result == "" {
		return "I couldn't generate a response."
	}
	return result
}

func (c *Client) stripBotMention(text string) string {
	mention := fmt.Sprintf("<@%s>", c.botUserID)
	text = strings.ReplaceAll(text, mention, "")
	return strings.TrimSpace(text)
}

func (c *Client) isAllowed(userID, channelID string) bool {
	cfg := c.clientDef.Config.Slack
	if len(cfg.AllowedUsers) == 0 && len(cfg.AllowedChannels) == 0 {
		return true
	}
	if len(cfg.AllowedUsers) > 0 && slices.Contains(cfg.AllowedUsers, userID) {
		return true
	}
	if len(cfg.AllowedChannels) > 0 && slices.Contains(cfg.AllowedChannels, channelID) {
		return true
	}
	return false
}

func (c *Client) getActiveAgentID(channelID string) string {
	c.activeAgentMu.RLock()
	defer c.activeAgentMu.RUnlock()
	if id, ok := c.activeAgent[channelID]; ok {
		return id
	}
	return c.clientDef.AllowedAgents[0]
}

func (c *Client) setActiveAgentID(channelID, agentID string) {
	c.activeAgentMu.Lock()
	defer c.activeAgentMu.Unlock()
	c.activeAgent[channelID] = agentID
}

func (c *Client) getAgentInfo(agentID string) *AgentInfo {
	for i := range c.agents {
		if c.agents[i].ID == agentID {
			return &c.agents[i]
		}
	}
	return nil
}
