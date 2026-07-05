package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

// heartbeatInterval mirrors the node's default; used to derive online status.
// Kept here (not read from node config) because the control plane only needs
// the window, and a machine's node may be configured independently.
const heartbeatInterval = 15 * time.Second

// MachineRepo is the persistence contract MachineService needs. The concrete
// *repository.MachineRepository satisfies it; the interface exists so the
// service is unit-testable with a fake.
type MachineRepo interface {
	Create(ctx context.Context, m *domain.Machine) error
	GetByID(ctx context.Context, id int64) (*domain.Machine, error)
	GetByName(ctx context.Context, name string) (*domain.Machine, error)
	List(ctx context.Context) ([]*domain.Machine, error)
	Update(ctx context.Context, m *domain.Machine) error
	Delete(ctx context.Context, id int64) error
	UpdateHeartbeat(ctx context.Context, id int64, version string, at time.Time) error
}

type MachineService struct {
	machines          MachineRepo
	agentPollInterval time.Duration
}

func NewMachineService(machines MachineRepo) *MachineService {
	return &MachineService{machines: machines}
}

// SetAgentPollInterval wires the global deploy.agent_poll_interval so
// ToMachineConfig can propagate it onto agent MachineConfigs.
func (s *MachineService) SetAgentPollInterval(d time.Duration) {
	s.agentPollInterval = d
}

type CreateMachineRequest struct {
	Name          string             `json:"name"           binding:"required"`
	MachineType   domain.Environment `json:"machine_type"`
	Host          string             `json:"host"`
	Port          int                `json:"port"`
	User          string             `json:"user"`
	AuthType      domain.AuthType    `json:"auth_type"`
	SSHKeyPath    string             `json:"ssh_key_path"`
	Password      string             `json:"password"`
	WorkspaceRoot string             `json:"workspace_root"`
	// agent mode
	Mode              domain.MachineMode `json:"mode"`
	AgentBaseURL      string             `json:"agent_base_url"`
	AgentProxyBaseURL string             `json:"agent_proxy_base_url"`
	AgentToken        string             `json:"agent_token"`
}

func (s *MachineService) Create(ctx context.Context, req CreateMachineRequest) (*domain.Machine, error) {
	req.MachineType = domain.DefaultEnvironment(req.MachineType)
	if !req.MachineType.Valid() {
		return nil, errors.New(fmt.Sprintf("invalid machine_type %q", req.MachineType))
	}
	if req.AuthType == "" {
		req.AuthType = domain.AuthTypeSSHKey
	}
	if req.Port == 0 {
		req.Port = 22
	}
	mode := req.Mode
	if mode == "" {
		mode = domain.MachineModeSSH
	}
	if err := validateMachineMode(mode, req.Host, req.AgentBaseURL); err != nil {
		return nil, err
	}
	m := &domain.Machine{
		Name:              req.Name,
		MachineType:       req.MachineType,
		Host:              req.Host,
		Port:              req.Port,
		User:              req.User,
		AuthType:          req.AuthType,
		SSHKeyPath:        req.SSHKeyPath,
		Password:          req.Password,
		WorkspaceRoot:     req.WorkspaceRoot,
		Mode:              mode,
		AgentBaseURL:      req.AgentBaseURL,
		AgentProxyBaseURL: req.AgentProxyBaseURL,
		AgentToken:        req.AgentToken,
	}
	if err := s.machines.Create(ctx, m); err != nil {
		return nil, err
	}
	logStruct("machine created", m)
	return m, nil
}

// validateMachineMode enforces the minimum fields each mode needs: SSH needs a
// Host, agent needs an AgentBaseURL.
func validateMachineMode(mode domain.MachineMode, host, agentBaseURL string) error {
	switch mode {
	case domain.MachineModeAgent:
		if agentBaseURL == "" {
			return errors.New("agent_base_url is required for agent-mode machines")
		}
	case domain.MachineModeSSH:
		if host == "" {
			return errors.New("host is required for ssh-mode machines")
		}
	default:
		return errors.New(fmt.Sprintf("invalid mode %q (want ssh or agent)", mode))
	}
	return nil
}

func (s *MachineService) Get(ctx context.Context, id int64) (*domain.Machine, error) {
	return s.machines.GetByID(ctx, id)
}

func (s *MachineService) GetByName(ctx context.Context, name string) (*domain.Machine, error) {
	return s.machines.GetByName(ctx, name)
}

func (s *MachineService) List(ctx context.Context) ([]*domain.Machine, error) {
	return s.machines.List(ctx)
}

type UpdateMachineRequest struct {
	Name          string             `json:"name"`
	MachineType   domain.Environment `json:"machine_type"`
	Host          string             `json:"host"`
	Port          int                `json:"port"`
	User          string             `json:"user"`
	AuthType      domain.AuthType    `json:"auth_type"`
	SSHKeyPath    string             `json:"ssh_key_path"`
	Password      string             `json:"password"`
	WorkspaceRoot *string            `json:"workspace_root,omitempty"`
	// agent mode (all optional; empty leaves the existing value unchanged)
	Mode              domain.MachineMode `json:"mode"`
	AgentBaseURL      *string            `json:"agent_base_url,omitempty"`
	AgentProxyBaseURL *string            `json:"agent_proxy_base_url,omitempty"`
	AgentToken        string             `json:"agent_token"`
}

func (s *MachineService) Update(ctx context.Context, id int64, req UpdateMachineRequest) (*domain.Machine, error) {
	m, err := s.machines.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Name != "" {
		m.Name = req.Name
	}
	if req.MachineType != "" {
		if !req.MachineType.Valid() {
			return nil, errors.New(fmt.Sprintf("invalid machine_type %q", req.MachineType))
		}
		m.MachineType = req.MachineType
	}
	if req.Host != "" {
		m.Host = req.Host
	}
	if req.Port > 0 {
		m.Port = req.Port
	}
	if req.User != "" {
		m.User = req.User
	}
	if req.AuthType != "" {
		m.AuthType = req.AuthType
	}
	if req.SSHKeyPath != "" {
		m.SSHKeyPath = req.SSHKeyPath
	}
	if req.Password != "" {
		m.Password = req.Password
	}
	if req.WorkspaceRoot != nil {
		m.WorkspaceRoot = *req.WorkspaceRoot
	}
	if req.Mode != "" {
		m.Mode = req.Mode
	}
	if req.AgentBaseURL != nil {
		m.AgentBaseURL = *req.AgentBaseURL
	}
	if req.AgentProxyBaseURL != nil {
		m.AgentProxyBaseURL = *req.AgentProxyBaseURL
	}
	if req.AgentToken != "" {
		m.AgentToken = req.AgentToken
	}
	if err := validateMachineMode(m.Mode, m.Host, m.AgentBaseURL); err != nil {
		return nil, err
	}
	if err := s.machines.Update(ctx, m); err != nil {
		return nil, err
	}
	logStruct("machine updated", m)
	return m, nil
}

// Heartbeat records an agent heartbeat after verifying the presented token
// matches the machine's agent_token. Returns an error the handler maps to 401
// (bad token) / 404 (unknown machine).
func (s *MachineService) Heartbeat(ctx context.Context, id int64, token, version string) error {
	m, err := s.machines.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if m.AgentToken == "" || m.AgentToken != token {
		return ErrInvalidCredentials
	}
	return s.machines.UpdateHeartbeat(ctx, id, version, time.Now())
}

func (s *MachineService) Delete(ctx context.Context, id int64) error {
	return s.machines.Delete(ctx, id)
}

func (s *MachineService) TestConnection(ctx context.Context, id int64) error {
	m, err := s.machines.GetByID(ctx, id)
	if err != nil {
		return err
	}
	// For an agent machine that has never heartbeated, the node is almost
	// certainly not up yet — give a clearer message than a raw dial error.
	if m.Mode == domain.MachineModeAgent && m.AgentLastHeartbeatAt == nil {
		return errors.New("agent has never heartbeated; is agenda-node running and reachable on this machine?")
	}
	mc := s.ToMachineConfig(m)
	r := runner.New(mc)
	var buf bytes.Buffer
	if err := r.RunShell(ctx, "", "echo ok", &buf); err != nil {
		return err
	}
	return nil
}

// ToMachineConfig adapts a DB-managed Machine to the runner-friendly
// MachineConfig, carrying the agent-mode fields so runner.New picks the right
// backend. Package-level (unbound) form uses no poll interval; prefer the method
// s.ToMachineConfig which also propagates the global poll interval.
func ToMachineConfig(m *domain.Machine) *config.MachineConfig {
	return &config.MachineConfig{
		MachineType:       string(m.MachineType),
		Host:              m.Host,
		Port:              m.Port,
		User:              m.User,
		SSHKeyPath:        m.SSHKeyPath,
		Password:          m.Password,
		WorkspaceRoot:     m.WorkspaceRoot,
		Mode:              string(m.Mode),
		AgentBaseURL:      m.AgentBaseURL,
		AgentProxyBaseURL: m.AgentProxyBaseURL,
		AgentToken:        m.AgentToken,
	}
}

// ToMachineConfig is the service method that additionally injects the global
// agent poll interval configured via SetAgentPollInterval.
func (s *MachineService) ToMachineConfig(m *domain.Machine) *config.MachineConfig {
	mc := ToMachineConfig(m)
	mc.AgentPollInterval = s.agentPollInterval
	return mc
}

// MachineView is the API representation of a Machine with the derived online
// flag (agent machines only) added. It embeds *domain.Machine so all existing
// json fields are preserved.
type MachineView struct {
	*domain.Machine
	Online bool `json:"online"`
}

func toView(m *domain.Machine) MachineView {
	return MachineView{Machine: m, Online: m.Online(heartbeatInterval)}
}

// ListViews returns machines with their derived online status for the API.
func (s *MachineService) ListViews(ctx context.Context) ([]MachineView, error) {
	machines, err := s.machines.List(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]MachineView, 0, len(machines))
	for _, m := range machines {
		views = append(views, toView(m))
	}
	return views, nil
}

// GetView returns one machine with its derived online status for the API.
func (s *MachineService) GetView(ctx context.Context, id int64) (MachineView, error) {
	m, err := s.machines.GetByID(ctx, id)
	if err != nil {
		return MachineView{}, err
	}
	return toView(m), nil
}
