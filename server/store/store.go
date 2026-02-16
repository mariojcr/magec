package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// Store manages agent, backend, and MCP configurations with JSON persistence.
type Store struct {
	mu       sync.RWMutex
	data     StoreData
	filePath string
	encryptionKey string

	changeMu    sync.Mutex
	changeSubs  []chan struct{}
}

// New creates a new Store. If filePath is non-empty and the file exists, it loads from it.
// The encryptionKey is used to encrypt/decrypt secret values at rest. If empty, secrets are stored in cleartext.
func New(filePath string, encryptionKey string) (*Store, error) {
	s := &Store{
		filePath:      filePath,
		encryptionKey: encryptionKey,
		data: StoreData{
			Backends:        []BackendDefinition{},
			MemoryProviders: []MemoryProvider{},
			MCPServers:      []MCPServer{},
			Agents:          []AgentDefinition{},
			Clients:         []ClientDefinition{},
			Flows:           []FlowDefinition{},
			Commands:        []Command{},
			Secrets:         []Secret{},
		},
	}

	if filePath != "" {
		if _, err := os.Stat(filePath); err == nil {
			if err := s.loadFromDisk(); err != nil {
				return nil, fmt.Errorf("failed to load store from %s: %w", filePath, err)
			}
		}
	}

	return s, nil
}

// OnChange returns a channel that receives a signal whenever the store is mutated.
// Multiple subscribers are supported. The channel is buffered (size 1) so a slow
// consumer won't block writers — at most one pending notification is kept.
func (s *Store) OnChange() <-chan struct{} {
	ch := make(chan struct{}, 1)
	s.changeMu.Lock()
	s.changeSubs = append(s.changeSubs, ch)
	s.changeMu.Unlock()
	return ch
}

// Data returns a copy of the current store data.
func (s *Store) Data() StoreData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

// --- Settings ---

// GetSettings returns the current global settings.
func (s *Store) GetSettings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Settings
}

// UpdateSettings replaces the global settings and persists.
func (s *Store) UpdateSettings(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Settings = settings
	return s.persist()
}

// --- Backends ---

func (s *Store) ListBackends() []BackendDefinition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]BackendDefinition, len(s.data.Backends))
	copy(result, s.data.Backends)
	return result
}

func (s *Store) GetBackend(id string) (BackendDefinition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, b := range s.data.Backends {
		if b.ID == id {
			return b, true
		}
	}
	return BackendDefinition{}, false
}

func (s *Store) CreateBackend(b BackendDefinition) (BackendDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b.ID = generateID()
	s.data.Backends = append(s.data.Backends, b)
	return b, s.persist()
}

func (s *Store) UpdateBackend(id string, b BackendDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Backends {
		if existing.ID == id {
			b.ID = id
			s.data.Backends[i] = b
			return s.persist()
		}
	}
	return fmt.Errorf("backend %q not found", id)
}

func (s *Store) DeleteBackend(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Backends {
		if existing.ID == id {
			s.data.Backends = append(s.data.Backends[:i], s.data.Backends[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("backend %q not found", id)
}

// --- Memory Providers ---

func (s *Store) ListMemoryProviders() []MemoryProvider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]MemoryProvider, len(s.data.MemoryProviders))
	copy(result, s.data.MemoryProviders)
	return result
}

func (s *Store) GetMemoryProvider(id string) (MemoryProvider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.data.MemoryProviders {
		if m.ID == id {
			return m, true
		}
	}
	return MemoryProvider{}, false
}

func (s *Store) CreateMemoryProvider(m MemoryProvider) (MemoryProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m.ID = generateID()
	s.data.MemoryProviders = append(s.data.MemoryProviders, m)
	return m, s.persist()
}

func (s *Store) UpdateMemoryProvider(id string, m MemoryProvider) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.MemoryProviders {
		if existing.ID == id {
			m.ID = id
			s.data.MemoryProviders[i] = m
			return s.persist()
		}
	}
	return fmt.Errorf("memory provider %q not found", id)
}

func (s *Store) DeleteMemoryProvider(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.MemoryProviders {
		if existing.ID == id {
			s.data.MemoryProviders = append(s.data.MemoryProviders[:i], s.data.MemoryProviders[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("memory provider %q not found", id)
}

// --- MCP Servers (global) ---

func (s *Store) ListMCPServers() []MCPServer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]MCPServer, len(s.data.MCPServers))
	copy(result, s.data.MCPServers)
	return result
}

func (s *Store) GetMCPServer(id string) (MCPServer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.data.MCPServers {
		if m.ID == id {
			return m, true
		}
	}
	return MCPServer{}, false
}

func (s *Store) CreateMCPServer(m MCPServer) (MCPServer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m.ID = generateID()
	s.data.MCPServers = append(s.data.MCPServers, m)
	return m, s.persist()
}

func (s *Store) UpdateMCPServer(id string, m MCPServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.MCPServers {
		if existing.ID == id {
			m.ID = id
			s.data.MCPServers[i] = m
			return s.persist()
		}
	}
	return fmt.Errorf("MCP server %q not found", id)
}

func (s *Store) DeleteMCPServer(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.MCPServers {
		if existing.ID == id {
			s.data.MCPServers = append(s.data.MCPServers[:i], s.data.MCPServers[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("MCP server %q not found", id)
}

// --- Agents ---

func (s *Store) ListAgents() []AgentDefinition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]AgentDefinition, len(s.data.Agents))
	copy(result, s.data.Agents)
	return result
}

func (s *Store) GetAgent(id string) (AgentDefinition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.data.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return AgentDefinition{}, false
}

func (s *Store) CreateAgent(a AgentDefinition) (AgentDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a.ID = generateID()
	if a.MCPServers == nil {
		a.MCPServers = []string{}
	}
	s.data.Agents = append(s.data.Agents, a)
	return a, s.persist()
}

func (s *Store) UpdateAgent(id string, a AgentDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Agents {
		if existing.ID == id {
			a.ID = id
			if a.MCPServers == nil {
				a.MCPServers = []string{}
			}
			s.data.Agents[i] = a
			return s.persist()
		}
	}
	return fmt.Errorf("agent %q not found", id)
}

func (s *Store) DeleteAgent(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Agents {
		if existing.ID == id {
			s.data.Agents = append(s.data.Agents[:i], s.data.Agents[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("agent %q not found", id)
}

// --- Agent MCP linking ---

// LinkAgentMCP adds an MCP server reference to an agent.
func (s *Store) LinkAgentMCP(agentID, mcpID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mcpExists := false
	for _, m := range s.data.MCPServers {
		if m.ID == mcpID {
			mcpExists = true
			break
		}
	}
	if !mcpExists {
		return fmt.Errorf("MCP server %q not found", mcpID)
	}

	for i, a := range s.data.Agents {
		if a.ID == agentID {
			if slices.Contains(a.MCPServers, mcpID) {
				return fmt.Errorf("MCP %q already linked to agent %q", mcpID, agentID)
			}
			s.data.Agents[i].MCPServers = append(s.data.Agents[i].MCPServers, mcpID)
			return s.persist()
		}
	}
	return fmt.Errorf("agent %q not found", agentID)
}

// UnlinkAgentMCP removes an MCP server reference from an agent.
func (s *Store) UnlinkAgentMCP(agentID, mcpID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, a := range s.data.Agents {
		if a.ID == agentID {
			idx := slices.Index(a.MCPServers, mcpID)
			if idx == -1 {
				return fmt.Errorf("MCP %q not linked to agent %q", mcpID, agentID)
			}
			s.data.Agents[i].MCPServers = slices.Delete(a.MCPServers, idx, idx+1)
			return s.persist()
		}
	}
	return fmt.Errorf("agent %q not found", agentID)
}

// ResolveAgentMCPs returns the full MCPServer definitions for an agent's linked MCPs.
func (s *Store) ResolveAgentMCPs(agentID string) ([]MCPServer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var agentMCPIDs []string
	found := false
	for _, a := range s.data.Agents {
		if a.ID == agentID {
			agentMCPIDs = a.MCPServers
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("agent %q not found", agentID)
	}

	mcpMap := make(map[string]MCPServer, len(s.data.MCPServers))
	for _, m := range s.data.MCPServers {
		mcpMap[m.ID] = m
	}

	result := make([]MCPServer, 0, len(agentMCPIDs))
	for _, id := range agentMCPIDs {
		if m, ok := mcpMap[id]; ok {
			result = append(result, m)
		}
	}
	return result, nil
}

// --- Clients ---

// generateToken creates a random API token with the "mgc_" prefix.
func generateToken() string {
	b := make([]byte, 20)
	rand.Read(b)
	return "mgc_" + hex.EncodeToString(b)
}

func (s *Store) ListClients() []ClientDefinition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]ClientDefinition, len(s.data.Clients))
	copy(result, s.data.Clients)
	return result
}

func (s *Store) GetClient(id string) (ClientDefinition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.data.Clients {
		if c.ID == id {
			return c, true
		}
	}
	return ClientDefinition{}, false
}

// GetClientByToken looks up a client by its API token. Used by the auth middleware.
func (s *Store) GetClientByToken(token string) (ClientDefinition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.data.Clients {
		if c.Token == token {
			return c, true
		}
	}
	return ClientDefinition{}, false
}

func (s *Store) CreateClient(c ClientDefinition) (ClientDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c.ID = generateID()
	c.Token = generateToken()
	if c.AllowedAgents == nil {
		c.AllowedAgents = []string{}
	}
	s.data.Clients = append(s.data.Clients, c)
	return c, s.persist()
}

func (s *Store) UpdateClient(id string, c ClientDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Clients {
		if existing.ID == id {
			c.ID = id
			c.Token = existing.Token
			if c.AllowedAgents == nil {
				c.AllowedAgents = []string{}
			}
			s.data.Clients[i] = c
			return s.persist()
		}
	}
	return fmt.Errorf("client %q not found", id)
}

func (s *Store) DeleteClient(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Clients {
		if existing.ID == id {
			s.data.Clients = append(s.data.Clients[:i], s.data.Clients[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("client %q not found", id)
}

// RegenerateClientToken replaces a client's API token with a new random one.
func (s *Store) RegenerateClientToken(id string) (ClientDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Clients {
		if existing.ID == id {
			s.data.Clients[i].Token = generateToken()
			return s.data.Clients[i], s.persist()
		}
	}
	return ClientDefinition{}, fmt.Errorf("client %q not found", id)
}

// --- Flows ---

func (s *Store) ListFlows() []FlowDefinition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]FlowDefinition, len(s.data.Flows))
	copy(result, s.data.Flows)
	return result
}

func (s *Store) GetFlow(id string) (FlowDefinition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, f := range s.data.Flows {
		if f.ID == id {
			return f, true
		}
	}
	return FlowDefinition{}, false
}

func (s *Store) CreateFlow(f FlowDefinition) (FlowDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f.ID = generateID()
	s.data.Flows = append(s.data.Flows, f)
	return f, s.persist()
}

func (s *Store) UpdateFlow(id string, f FlowDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Flows {
		if existing.ID == id {
			f.ID = id
			s.data.Flows[i] = f
			return s.persist()
		}
	}
	return fmt.Errorf("flow %q not found", id)
}

func (s *Store) DeleteFlow(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Flows {
		if existing.ID == id {
			s.data.Flows = append(s.data.Flows[:i], s.data.Flows[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("flow %q not found", id)
}

// --- Commands ---

func (s *Store) ListCommands() []Command {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Command, len(s.data.Commands))
	copy(result, s.data.Commands)
	return result
}

func (s *Store) GetCommand(id string) (Command, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.data.Commands {
		if c.ID == id {
			return c, true
		}
	}
	return Command{}, false
}

func (s *Store) CreateCommand(c Command) (Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c.ID = generateID()
	s.data.Commands = append(s.data.Commands, c)
	return c, s.persist()
}

func (s *Store) UpdateCommand(id string, c Command) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Commands {
		if existing.ID == id {
			c.ID = id
			s.data.Commands[i] = c
			return s.persist()
		}
	}
	return fmt.Errorf("command %q not found", id)
}

func (s *Store) DeleteCommand(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Commands {
		if existing.ID == id {
			s.data.Commands = append(s.data.Commands[:i], s.data.Commands[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("command %q not found", id)
}

// --- Persistence ---

// --- Secrets ---

func (s *Store) ListSecrets() []Secret {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Secret, len(s.data.Secrets))
	copy(result, s.data.Secrets)
	return result
}

func (s *Store) GetSecret(id string) (Secret, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sec := range s.data.Secrets {
		if sec.ID == id {
			return sec, true
		}
	}
	return Secret{}, false
}

func (s *Store) CreateSecret(sec Secret) (Secret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.data.Secrets {
		if existing.Key == sec.Key {
			return Secret{}, fmt.Errorf("secret with key %q already exists", sec.Key)
		}
	}

	sec.ID = generateID()
	if s.encryptionKey != "" && sec.Value != "" && !isEncrypted(sec.Value) {
		enc, err := encryptValue(sec.Value, s.encryptionKey)
		if err != nil {
			return Secret{}, fmt.Errorf("encrypt: %w", err)
		}
		sec.Value = enc
	}
	s.data.Secrets = append(s.data.Secrets, sec)
	return sec, s.persist()
}

func (s *Store) UpdateSecret(id string, sec Secret) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.data.Secrets {
		if existing.Key == sec.Key && existing.ID != id {
			return fmt.Errorf("secret with key %q already exists", sec.Key)
		}
	}

	for i, existing := range s.data.Secrets {
		if existing.ID == id {
			sec.ID = id
			if s.encryptionKey != "" && sec.Value != "" && !isEncrypted(sec.Value) {
				enc, err := encryptValue(sec.Value, s.encryptionKey)
				if err != nil {
					return fmt.Errorf("encrypt: %w", err)
				}
				sec.Value = enc
			}
			s.data.Secrets[i] = sec
			return s.persist()
		}
	}
	return fmt.Errorf("secret %q not found", id)
}

func (s *Store) DeleteSecret(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.data.Secrets {
		if existing.ID == id {
			s.data.Secrets = append(s.data.Secrets[:i], s.data.Secrets[i+1:]...)
			return s.persist()
		}
	}
	return fmt.Errorf("secret %q not found", id)
}

// --- Persistence (internal) ---

// persist writes the current store data to disk as formatted JSON and
// notifies all change subscribers.
func (s *Store) persist() error {
	if s.filePath == "" {
		return nil
	}

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create store directory: %w", err)
	}

	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal store data: %w", err)
	}

	if err := os.WriteFile(s.filePath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write store file: %w", err)
	}

	s.notifyChange()
	return nil
}

// notifyChange sends a non-blocking signal to all OnChange subscribers.
func (s *Store) notifyChange() {
	s.changeMu.Lock()
	defer s.changeMu.Unlock()
	for _, ch := range s.changeSubs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// loadFromDisk reads the store file, extracts secrets and injects them as
// environment variables, then expands all env vars and unmarshals the final data.
// This two-pass approach lets secrets reference each other or be used in other fields.
func (s *Store) loadFromDisk() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	var raw StoreData
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	for _, sec := range raw.Secrets {
		if sec.Key != "" && sec.Value != "" {
			val := sec.Value
			if s.encryptionKey != "" && isEncrypted(val) {
				decrypted, err := decryptValue(val, s.encryptionKey)
				if err != nil {
					return fmt.Errorf("failed to decrypt secret %q: %w", sec.Key, err)
				}
				val = decrypted
			}
			os.Setenv(sec.Key, val)
		}
	}

	expanded := os.ExpandEnv(string(data))

	var storeData StoreData
	if err := json.Unmarshal([]byte(expanded), &storeData); err != nil {
		return err
	}

	if storeData.Backends == nil {
		storeData.Backends = []BackendDefinition{}
	}
	if storeData.MemoryProviders == nil {
		storeData.MemoryProviders = []MemoryProvider{}
	}
	if storeData.MCPServers == nil {
		storeData.MCPServers = []MCPServer{}
	}
	if storeData.Agents == nil {
		storeData.Agents = []AgentDefinition{}
	}
	if storeData.Clients == nil {
		storeData.Clients = []ClientDefinition{}
	}
	if storeData.Flows == nil {
		storeData.Flows = []FlowDefinition{}
	}
	if storeData.Commands == nil {
		storeData.Commands = []Command{}
	}
	if storeData.Secrets == nil {
		storeData.Secrets = []Secret{}
	}

	s.data = storeData

	return nil
}
