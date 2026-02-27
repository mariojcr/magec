package discord

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/achetronic/magec/server/clients/msgutil"
	"github.com/achetronic/magec/server/store"
)

const (
	ResponseModeText   = "text"
	ResponseModeVoice  = "voice"
	ResponseModeMirror = "mirror"
	ResponseModeBoth   = "both"
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

	session *discordgo.Session
	cancel  context.CancelFunc

	activeAgentMu sync.RWMutex
	activeAgent   map[string]string // channelID -> agentID

	responseMu           sync.RWMutex
	responseModeOverride string

	showToolsMu sync.RWMutex
	showTools   bool
}

func New(clientDef store.ClientDefinition, agentURL string, agents []AgentInfo, logger *slog.Logger) (*Client, error) {
	if clientDef.Config.Discord == nil {
		return nil, fmt.Errorf("discord config is required")
	}
	if clientDef.Config.Discord.BotToken == "" {
		return nil, fmt.Errorf("discord bot token is required")
	}

	session, err := discordgo.New("Bot " + clientDef.Config.Discord.BotToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentGuildMessages |
		discordgo.IntentDirectMessages |
		discordgo.IntentMessageContent |
		discordgo.IntentGuildMessageReactions |
		discordgo.IntentDirectMessageReactions

	return &Client{
		session:     session,
		clientDef:   clientDef,
		agentURL:    agentURL,
		agents:      agents,
		activeAgent: make(map[string]string),
		logger:      logger,
	}, nil
}

func (c *Client) Start(ctx context.Context) error {
	_, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.session.AddHandler(c.onMessageCreate)

	if err := c.session.Open(); err != nil {
		cancel()
		return fmt.Errorf("failed to open discord gateway: %w", err)
	}

	c.logger.Info("Discord bot started", "username", c.session.State.User.Username, "id", c.session.State.User.ID)

	<-ctx.Done()
	return nil
}

func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.session != nil {
		c.session.Close()
	}
	c.logger.Info("Discord bot stopped")
}

func (c *Client) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.ID == s.State.User.ID || m.Author.Bot {
		return
	}

	if !c.isAllowed(m.Author.ID, m.ChannelID) {
		c.logger.Debug("Unauthorized Discord message", "user", m.Author.ID, "channel", m.ChannelID)
		return
	}

	isDM := m.GuildID == ""
	isMention := false
	if !isDM {
		for _, mention := range m.Mentions {
			if mention.ID == s.State.User.ID {
				isMention = true
				break
			}
		}
		if !isMention {
			return
		}
	}

	text := c.stripBotMention(m.Content, s.State.User.ID)

	isVoiceMessage := m.Flags&discordgo.MessageFlagsIsVoiceMessage != 0
	if !isVoiceMessage {
		for _, att := range m.Attachments {
			if strings.HasPrefix(att.ContentType, "audio/") && att.DurationSecs > 0 {
				isVoiceMessage = true
				break
			}
		}
	}

	if isVoiceMessage {
		c.handleVoice(s, m)
		return
	}

	if c.handleBotCommand(s, m, text) {
		return
	}

	if text == "" && len(m.Attachments) == 0 {
		return
	}

	c.handleTextMessage(s, m, text)
}

// resolveThread returns the channel ID where replies should be sent.
//
// Rules:
//   - DM (no guild): reply in the same DM channel
//   - Message already in a thread: reply in that same thread
//   - Message in a regular guild channel: create a new thread anchored to that
//     message and reply inside it
func (c *Client) resolveThread(s *discordgo.Session, m *discordgo.MessageCreate, threadName string) string {
	if m.GuildID == "" {
		return m.ChannelID
	}

	ch, err := s.Channel(m.ChannelID)
	if err != nil {
		c.logger.Error("Failed to get channel info", "error", err)
		return m.ChannelID
	}

	if isDiscordThread(ch.Type) {
		return m.ChannelID
	}

	name := threadName
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:37]) + "..."
	}
	if name == "" {
		name = "Chat with Magec"
		if info := c.getAgentInfo(c.getActiveAgentID(m.ChannelID)); info != nil && info.Name != "" {
			name = "Chat with " + info.Name
		}
	}

	th, err := s.MessageThreadStartComplex(m.ChannelID, m.ID, &discordgo.ThreadStart{
		Name:                name,
		AutoArchiveDuration: 60,
		Type:                discordgo.ChannelTypeGuildPublicThread,
	})
	if err != nil {
		c.logger.Error("Failed to create thread", "error", err)
		return m.ChannelID
	}

	return th.ID
}

func (c *Client) handleTextMessage(s *discordgo.Session, m *discordgo.MessageCreate, text string) {
	c.logger.Info("Discord message received",
		"user", m.Author.Username,
		"channel", m.ChannelID,
		"text", text,
	)

	c.addReaction(s, m.ChannelID, m.ID, "👀")

	targetID := c.resolveThread(s, m, text)
	agentID := c.getActiveAgentID(targetID)

	s.ChannelTyping(targetID)
	c.addReaction(s, m.ChannelID, m.ID, "🧠")

	typingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-typingDone:
				return
			case <-ticker.C:
				s.ChannelTyping(targetID)
			}
		}
	}()

	inputText, truncated := msgutil.ValidateInputLength(text, msgutil.DefaultMaxInputLength)
	if truncated {
		c.logger.Warn("Inbound message truncated", "channel", targetID, "original_len", len([]rune(text)))
	}

	sessionID := c.buildSessionID(targetID, agentID)
	userIDStr := "default_user"

	artifactsBefore := c.listArtifacts(agentID, userIDStr, sessionID)

	firstText := true
	hasText := false
	hasToolActivity := false
	var lastFinishReason string
	var lastErrorMessage string
	toolCount := 0
	var toolCounterMsgID string

	err := c.callAgentSSE(m, targetID, agentID, sessionID, inputText, func(evt msgutil.SSEEvent) {
		if evt.FinishReason != "" {
			lastFinishReason = evt.FinishReason
		}
		if evt.ErrorMessage != "" {
			lastErrorMessage = evt.ErrorMessage
		}
		switch evt.Type {
		case msgutil.SSEEventText:
			hasText = true
			toolCount = 0
			toolCounterMsgID = ""
			for i, chunk := range msgutil.SplitMessage(evt.Text, msgutil.DiscordMaxMessageLength) {
				msg := &discordgo.MessageSend{Content: chunk}
				if firstText && i == 0 {
					msg.Reference = &discordgo.MessageReference{MessageID: m.ID, ChannelID: m.ChannelID, GuildID: m.GuildID}
					firstText = false
				}
				if _, err := s.ChannelMessageSendComplex(targetID, msg); err != nil {
					c.logger.Error("Failed to send message", "error", err)
					break
				}
			}
		case msgutil.SSEEventToolCall:
			hasToolActivity = true
			toolCounterMsgID = c.sendToolCounter(s, targetID, toolCounterMsgID, &toolCount, evt)
		case msgutil.SSEEventToolResult:
			hasToolActivity = true
			if c.getShowTools() {
				s.ChannelMessageSend(targetID, msgutil.FormatToolResultDiscord(evt))
			}
		}
	})
	close(typingDone)

	if err != nil {
		c.logger.Error("Failed to call agent", "error", err)
		c.addReaction(s, m.ChannelID, m.ID, "❌")
		s.ChannelMessageSend(targetID, fmt.Sprintf("Failed to process your request: %s", sanitizeError(err)))
		return
	}

	if !hasText && !hasToolActivity {
		s.ChannelMessageSend(targetID, msgutil.ExplainNoResponse(lastFinishReason, lastErrorMessage))
	}

	c.addReaction(s, m.ChannelID, m.ID, "✅")
	c.sendNewArtifacts(s, targetID, agentID, userIDStr, sessionID, artifactsBefore)
}

func (c *Client) handleVoice(s *discordgo.Session, m *discordgo.MessageCreate) {
	var audioAttachment *discordgo.MessageAttachment
	for _, att := range m.Attachments {
		if strings.HasPrefix(att.ContentType, "audio/") {
			audioAttachment = att
			break
		}
	}
	if audioAttachment == nil {
		return
	}

	c.logger.Info("Discord voice message received",
		"user", m.Author.Username,
		"channel", m.ChannelID,
		"duration", audioAttachment.DurationSecs,
	)

	c.addReaction(s, m.ChannelID, m.ID, "👀")

	targetID := c.resolveThread(s, m, "")
	agentID := c.getActiveAgentID(targetID)

	s.ChannelTyping(targetID)

	audioData, err := c.downloadFile(audioAttachment.URL)
	if err != nil {
		c.logger.Error("Failed to download voice attachment", "error", err)
		c.addReaction(s, m.ChannelID, m.ID, "❌")
		s.ChannelMessageSend(targetID, "Failed to download your voice message. Please try again.")
		return
	}

	wavData, err := c.convertAudioToWav(audioData)
	if err != nil {
		c.logger.Error("Failed to convert audio", "error", err)
		c.addReaction(s, m.ChannelID, m.ID, "❌")
		s.ChannelMessageSend(targetID, "Failed to process your voice message. Please try again.")
		return
	}

	text, err := c.transcribeAudio(wavData, agentID)
	if err != nil {
		c.logger.Error("Failed to transcribe audio", "error", err)
		c.addReaction(s, m.ChannelID, m.ID, "❌")
		s.ChannelMessageSend(targetID, "Sorry, I couldn't transcribe your voice message.")
		return
	}

	c.logger.Info("Transcribed voice", "text", text)
	c.addReaction(s, m.ChannelID, m.ID, "🧠")

	typingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-typingDone:
				return
			case <-ticker.C:
				s.ChannelTyping(targetID)
			}
		}
	}()

	voiceInput, truncated := msgutil.ValidateInputLength(text, msgutil.DefaultMaxInputLength)
	if truncated {
		c.logger.Warn("Transcribed voice message truncated", "channel", targetID, "original_len", len([]rune(text)))
	}

	sessionID := c.buildSessionID(targetID, agentID)
	userIDStr := "default_user"

	artifactsBefore := c.listArtifacts(agentID, userIDStr, sessionID)

	firstText := true
	var lastTextResponse string
	hasText := false
	hasToolActivity := false
	var lastFinishReason string
	var lastErrorMessage string
	toolCount := 0
	var toolCounterMsgID string

	err = c.callAgentSSE(m, targetID, agentID, sessionID, voiceInput, func(evt msgutil.SSEEvent) {
		if evt.FinishReason != "" {
			lastFinishReason = evt.FinishReason
		}
		if evt.ErrorMessage != "" {
			lastErrorMessage = evt.ErrorMessage
		}
		switch evt.Type {
		case msgutil.SSEEventText:
			hasText = true
			lastTextResponse = evt.Text
			toolCount = 0
			toolCounterMsgID = ""
			mode := c.getResponseMode()
			if mode == ResponseModeText || mode == ResponseModeBoth {
				for i, chunk := range msgutil.SplitMessage(evt.Text, msgutil.DiscordMaxMessageLength) {
					msg := &discordgo.MessageSend{Content: chunk}
					if firstText && i == 0 {
						msg.Reference = &discordgo.MessageReference{MessageID: m.ID, ChannelID: m.ChannelID, GuildID: m.GuildID}
						firstText = false
					}
					s.ChannelMessageSendComplex(targetID, msg)
				}
			}
		case msgutil.SSEEventToolCall:
			hasToolActivity = true
			toolCounterMsgID = c.sendToolCounter(s, targetID, toolCounterMsgID, &toolCount, evt)
		case msgutil.SSEEventToolResult:
			hasToolActivity = true
			if c.getShowTools() {
				s.ChannelMessageSend(targetID, msgutil.FormatToolResultDiscord(evt))
			}
		}
	})
	close(typingDone)

	if err != nil {
		c.logger.Error("Failed to call agent", "error", err)
		c.addReaction(s, m.ChannelID, m.ID, "❌")
		s.ChannelMessageSend(targetID, fmt.Sprintf("Failed to process your request: %s", sanitizeError(err)))
		return
	}

	if !hasText && !hasToolActivity {
		s.ChannelMessageSend(targetID, msgutil.ExplainNoResponse(lastFinishReason, lastErrorMessage))
	}

	mode := c.getResponseMode()
	if (mode == ResponseModeVoice || mode == ResponseModeBoth || mode == ResponseModeMirror) && lastTextResponse != "" {
		c.sendVoiceResponse(s, targetID, lastTextResponse, agentID)
	}

	c.addReaction(s, m.ChannelID, m.ID, "✅")
	c.sendNewArtifacts(s, targetID, agentID, userIDStr, sessionID, artifactsBefore)
}

// sendToolCounter posts or edits a compact tool activity counter in the target channel.
// Returns the updated message ID for subsequent edits.
func (c *Client) sendToolCounter(s *discordgo.Session, channelID, counterMsgID string, toolCount *int, evt msgutil.SSEEvent) string {
	if c.getShowTools() {
		s.ChannelMessageSend(channelID, msgutil.FormatToolCallDiscord(evt))
		return counterMsgID
	}

	*toolCount++
	counterText := fmt.Sprintf("⚙️ x%d", *toolCount)

	if counterMsgID == "" {
		sent, err := s.ChannelMessageSend(channelID, counterText)
		if err == nil {
			return sent.ID
		}
		return ""
	}

	s.ChannelMessageEdit(channelID, counterMsgID, counterText)
	return counterMsgID
}

func (c *Client) handleBotCommand(s *discordgo.Session, m *discordgo.MessageCreate, text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))

	if !strings.HasPrefix(lower, "!") {
		return false
	}
	lower = lower[1:]
	text = strings.TrimSpace(text[1:])

	if lower == "help" {
		helpText := "**Available commands:**\n" +
			"• `!help` — Show this help message\n" +
			"• `!agent` — Show or switch the active agent\n" +
			"• `!agent <id>` — Switch to a specific agent\n" +
			"• `!reset` — Reset the conversation session\n" +
			"• `!responsemode` — Show or change the response mode\n" +
			"• `!responsemode <mode>` — Set response mode (`text`, `voice`, `mirror`, `both`, `reset`)\n" +
			"• `!showtools` — Toggle tool call visibility"
		s.ChannelMessageSend(m.ChannelID, helpText)
		return true
	}

	if lower == "agent" {
		currentID := c.getActiveAgentID(m.ChannelID)
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
		msg := fmt.Sprintf("**Active agent:** %s\n\n**Available agents:**\n%s\nUsage: `!agent <id>`", currentLabel, agentList)
		s.ChannelMessageSend(m.ChannelID, msg)
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
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Unknown agent `%s`. Available: %s", agentID, strings.Join(ids, ", ")))
			return true
		}
		c.setActiveAgentID(m.ChannelID, agentID)
		agent := c.getAgentInfo(agentID)
		label := agentID
		if agent != nil && agent.Name != "" {
			label = agent.Name
		}
		c.logger.Info("Discord agent switched", "channel", m.ChannelID, "agent", agentID)
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Switched to agent **%s** (`%s`)", label, agentID))
		return true
	}

	if lower == "reset" {
		agentID := c.getActiveAgentID(m.ChannelID)
		sessionID := c.buildSessionID(m.ChannelID, agentID)
		if err := c.deleteSession(agentID, sessionID); err != nil {
			c.logger.Error("Failed to delete session", "error", err)
			s.ChannelMessageSend(m.ChannelID, "Failed to reset session.")
			return true
		}
		c.logger.Info("Session reset", "channel", m.ChannelID, "agent", agentID, "session", sessionID)
		agent := c.getAgentInfo(agentID)
		label := agentID
		if agent != nil && agent.Name != "" {
			label = agent.Name
		}
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Session reset for **%s**. Next message starts a fresh conversation.", label))
		return true
	}

	if lower == "responsemode" {
		current := c.getResponseMode()
		c.responseMu.RLock()
		overridden := c.responseModeOverride != ""
		c.responseMu.RUnlock()

		status := fmt.Sprintf("**Response mode:** `%s`", current)
		if overridden {
			status += fmt.Sprintf(" (override, config: `%s`)", c.clientDef.Config.Discord.ResponseMode)
		}
		status += "\n**Options:** `text`, `voice`, `mirror`, `both`, `reset`"
		s.ChannelMessageSend(m.ChannelID, status)
		return true
	}

	if strings.HasPrefix(lower, "responsemode ") {
		arg := strings.TrimSpace(text[13:])
		return c.handleResponseModeCommand(s, m.ChannelID, arg)
	}

	if lower == "showtools" {
		c.showToolsMu.Lock()
		c.showTools = !c.showTools
		state := c.showTools
		c.showToolsMu.Unlock()
		label := "OFF"
		if state {
			label = "ON"
		}
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("🔧 Tool call visibility: **%s**", label))
		return true
	}

	return false
}

func (c *Client) handleResponseModeCommand(s *discordgo.Session, channelID, arg string) bool {
	validModes := []string{ResponseModeText, ResponseModeVoice, ResponseModeMirror, ResponseModeBoth}

	if arg == "reset" {
		c.responseMu.Lock()
		c.responseModeOverride = ""
		c.responseMu.Unlock()
		c.logger.Info("Response mode override cleared", "config_mode", c.clientDef.Config.Discord.ResponseMode)
		s.ChannelMessageSend(channelID, fmt.Sprintf("Response mode reset to config default: `%s`", c.clientDef.Config.Discord.ResponseMode))
		return true
	}

	if !slices.Contains(validModes, arg) {
		s.ChannelMessageSend(channelID, fmt.Sprintf("Invalid mode `%s`. Valid options: `text`, `voice`, `mirror`, `both`, `reset`", arg))
		return true
	}

	c.responseMu.Lock()
	c.responseModeOverride = arg
	c.responseMu.Unlock()

	c.logger.Info("Response mode overridden", "new_mode", arg)
	s.ChannelMessageSend(channelID, fmt.Sprintf("Response mode set to `%s` (until restart)", arg))
	return true
}

func (c *Client) sendVoiceResponse(s *discordgo.Session, channelID, text, agentID string) {
	audioData, err := c.generateTTS(text, agentID)
	if err != nil {
		c.logger.Error("Failed to generate TTS", "error", err)
		return
	}

	_, err = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Files: []*discordgo.File{
			{
				Name:        "voice.ogg",
				ContentType: "audio/ogg",
				Reader:      bytes.NewReader(audioData),
			},
		},
	})
	if err != nil {
		c.logger.Error("Failed to send voice file", "error", err)
	}
}

func (c *Client) getResponseMode() string {
	c.responseMu.RLock()
	defer c.responseMu.RUnlock()
	if c.responseModeOverride != "" {
		return c.responseModeOverride
	}
	mode := c.clientDef.Config.Discord.ResponseMode
	if mode == "" {
		return ResponseModeText
	}
	return mode
}

func (c *Client) getShowTools() bool {
	c.showToolsMu.RLock()
	defer c.showToolsMu.RUnlock()
	return c.showTools
}

// buildSessionID builds a stable session ID scoped to a channel/thread + agent.
// Uses the same format as Slack: discord_{channelID}_{agentID}.
func (c *Client) buildSessionID(channelID, agentID string) string {
	return fmt.Sprintf("discord_%s_%s", channelID, agentID)
}

func (c *Client) callAgentSSE(m *discordgo.MessageCreate, targetID, agentID, sessionID, message string, handler func(msgutil.SSEEvent)) error {
	if err := c.ensureSession(agentID, "default_user", sessionID); err != nil {
		c.logger.Warn("Failed to ensure session, continuing anyway", "error", err)
	}

	fullMessage := c.buildMessageContext(m, targetID) + c.fetchThreadContext(targetID, m.ID) + message

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
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", c.agentURL+"/run_sse", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	c.setAuthHeader(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call agent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agent returned status %d: %s", resp.StatusCode, string(body))
	}

	return msgutil.ParseSSEStream(resp.Body, handler)
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

// fetchThreadContext returns prior messages in a thread as context for the agent.
// Only called when targetID is a thread channel; returns empty string otherwise.
// Excludes the current message (identified by currentMsgID) to avoid duplication.
func (c *Client) fetchThreadContext(targetID, currentMsgID string) string {
	ch, err := c.session.Channel(targetID)
	if err != nil {
		c.logger.Debug("Failed to get channel info for thread context", "error", err)
		return ""
	}
	if !isDiscordThread(ch.Type) {
		return ""
	}

	msgs, err := c.session.ChannelMessages(targetID, 20, currentMsgID, "", "")
	if err != nil {
		c.logger.Debug("Failed to fetch thread context", "error", err)
		return ""
	}
	if len(msgs) == 0 {
		return ""
	}

	// ChannelMessages returns newest-first; reverse for chronological order.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	var sb strings.Builder
	sb.WriteString("<!--MAGEC_THREAD_HISTORY:\n")
	for _, msg := range msgs {
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		if msg.Author.ID == c.session.State.User.ID {
			text = c.stripBotMention(text, c.session.State.User.ID)
		}
		name := msg.Author.Username
		if msg.Author.GlobalName != "" {
			name = msg.Author.GlobalName
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", name, text))
	}
	sb.WriteString(":MAGEC_THREAD_HISTORY-->\n")
	return sb.String()
}

func (c *Client) buildMessageContext(m *discordgo.MessageCreate, targetID string) string {
	meta := map[string]interface{}{
		"source":             "discord",
		"discord_user_id":    m.Author.ID,
		"discord_channel_id": targetID,
	}

	if m.Author.Username != "" {
		meta["discord_username"] = m.Author.Username
	}
	if m.Author.GlobalName != "" {
		meta["discord_name"] = m.Author.GlobalName
	}
	if m.GuildID != "" {
		meta["discord_guild_id"] = m.GuildID
		meta["discord_channel_type"] = "guild"
	} else {
		meta["discord_channel_type"] = "dm"
	}

	jsonBytes, err := json.Marshal(meta)
	if err != nil {
		c.logger.Warn("Failed to marshal message context metadata", "error", err)
		return ""
	}
	return fmt.Sprintf("<!--MAGEC_META:%s:MAGEC_META-->\n", string(jsonBytes))
}

func (c *Client) setAuthHeader(req *http.Request) {
	if c.clientDef.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.clientDef.Token)
	}
}

func (c *Client) isAllowed(userID, channelID string) bool {
	cfg := c.clientDef.Config.Discord
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

func (c *Client) stripBotMention(text, botID string) string {
	text = strings.ReplaceAll(text, fmt.Sprintf("<@%s>", botID), "")
	text = strings.ReplaceAll(text, fmt.Sprintf("<@!%s>", botID), "")
	return strings.TrimSpace(text)
}

func (c *Client) addReaction(s *discordgo.Session, channelID, messageID, emoji string) {
	if err := s.MessageReactionAdd(channelID, messageID, emoji); err != nil {
		c.logger.Debug("Failed to add reaction", "emoji", emoji, "error", err)
	}
}

func (c *Client) downloadFile(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func (c *Client) convertAudioToWav(audioData []byte) ([]byte, error) {
	cmd := exec.Command("ffmpeg",
		"-i", "pipe:0",
		"-ar", "16000",
		"-ac", "1",
		"-f", "wav",
		"pipe:1",
	)

	cmd.Stdin = bytes.NewReader(audioData)
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

func (c *Client) sendNewArtifacts(s *discordgo.Session, channelID, agentID, userID, sessionID string, before []string) {
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

		_, err = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Files: []*discordgo.File{
				{
					Name:   name,
					Reader: bytes.NewReader(data),
				},
			},
		})
		if err != nil {
			c.logger.Error("Failed to send artifact", "name", name, "error", err)
		}
	}
}

// isDiscordThread returns true for any thread channel type.
func isDiscordThread(t discordgo.ChannelType) bool {
	return t == discordgo.ChannelTypeGuildNewsThread ||
		t == discordgo.ChannelTypeGuildPublicThread ||
		t == discordgo.ChannelTypeGuildPrivateThread
}

func sanitizeError(err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	for _, secret := range []string{"Bearer ", "Bot ", "token"} {
		if strings.Contains(strings.ToLower(msg), strings.ToLower(secret)) {
			return "an internal error occurred"
		}
	}
	return msg
}
