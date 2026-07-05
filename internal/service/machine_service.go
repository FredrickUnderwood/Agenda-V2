package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

type MachineService struct {
	machines *repository.MachineRepository
}

func NewMachineService(machines *repository.MachineRepository) *MachineService {
	return &MachineService{machines: machines}
}

type CreateMachineRequest struct {
	Name          string             `json:"name"           binding:"required"`
	MachineType   domain.Environment `json:"machine_type"`
	Host          string             `json:"host"           binding:"required"`
	Port          int                `json:"port"`
	User          string             `json:"user"`
	AuthType      domain.AuthType    `json:"auth_type"`
	SSHKeyPath    string             `json:"ssh_key_path"`
	Password      string             `json:"password"`
	WorkspaceRoot string             `json:"workspace_root"`
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
	m := &domain.Machine{
		Name:          req.Name,
		MachineType:   req.MachineType,
		Host:          req.Host,
		Port:          req.Port,
		User:          req.User,
		AuthType:      req.AuthType,
		SSHKeyPath:    req.SSHKeyPath,
		Password:      req.Password,
		WorkspaceRoot: req.WorkspaceRoot,
	}
	if err := s.machines.Create(ctx, m); err != nil {
		return nil, err
	}
	logStruct("machine created", m)
	return m, nil
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
	if err := s.machines.Update(ctx, m); err != nil {
		return nil, err
	}
	logStruct("machine updated", m)
	return m, nil
}

func (s *MachineService) Delete(ctx context.Context, id int64) error {
	return s.machines.Delete(ctx, id)
}

func (s *MachineService) TestConnection(ctx context.Context, id int64) error {
	m, err := s.machines.GetByID(ctx, id)
	if err != nil {
		return err
	}
	mc := ToMachineConfig(m)
	r := runner.New(mc)
	var buf bytes.Buffer
	if err := r.RunShell(ctx, "", "echo ok", &buf); err != nil {
		return err
	}
	return nil
}

// ToMachineConfig adapts a DB-managed Machine to the runner-friendly MachineConfig.
func ToMachineConfig(m *domain.Machine) *config.MachineConfig {
	return &config.MachineConfig{
		MachineType:   string(m.MachineType),
		Host:          m.Host,
		Port:          m.Port,
		User:          m.User,
		SSHKeyPath:    m.SSHKeyPath,
		Password:      m.Password,
		WorkspaceRoot: m.WorkspaceRoot,
	}
}
