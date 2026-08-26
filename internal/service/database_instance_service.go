package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/secret"
)

// ErrDatabaseInstanceNotAgent is returned when an instance is bound to a
// machine the control plane cannot relay a query through.
var ErrDatabaseInstanceNotAgent = errors.New("this database's machine does not run agenda-node; queries are relayed through the node agent, so only agent-mode machines can host a registered database")

// DatabaseInstanceService owns registration of queryable databases: CRUD, and
// resolving an instance to the credentials and node endpoint a query needs.
//
// Passwords are encrypted at rest with the same secret.Box the machine agent
// tokens use, and are decrypted only on the path to the node.
type DatabaseInstanceService struct {
	instances *repository.DatabaseInstanceRepository
	machines  machineGetter
	box       *secret.Box
}

func NewDatabaseInstanceService(
	instances *repository.DatabaseInstanceRepository,
	machines machineGetter,
	box *secret.Box,
) *DatabaseInstanceService {
	return &DatabaseInstanceService{instances: instances, machines: machines, box: box}
}

type CreateDatabaseInstanceRequest struct {
	Name            string                `json:"name"       binding:"required"`
	Engine          domain.DatabaseEngine `json:"engine"`
	MachineID       int64                 `json:"machine_id" binding:"required"`
	Port            int                   `json:"port"`
	Username        string                `json:"username"`
	Password        string                `json:"password"`
	DefaultDatabase string                `json:"default_database"`
	Env             domain.Environment    `json:"env"`
	Description     string                `json:"description"`
	Enabled         *bool                 `json:"enabled"`
}

// UpdateDatabaseInstanceRequest leaves any omitted field unchanged. Password is
// a plain string rather than a pointer on purpose: an empty password means
// "keep the stored one", matching how MachineService.Update treats agent_token,
// so an operator editing the port never has to retype the credentials.
type UpdateDatabaseInstanceRequest struct {
	Name            string                `json:"name"`
	Engine          domain.DatabaseEngine `json:"engine"`
	MachineID       int64                 `json:"machine_id"`
	Port            int                   `json:"port"`
	Username        string                `json:"username"`
	Password        string                `json:"password"`
	DefaultDatabase *string               `json:"default_database,omitempty"`
	Env             domain.Environment    `json:"env"`
	Description     *string               `json:"description,omitempty"`
	Enabled         *bool                 `json:"enabled,omitempty"`
}

func (s *DatabaseInstanceService) Create(ctx context.Context, req CreateDatabaseInstanceRequest) (*domain.DatabaseInstance, error) {
	inst := &domain.DatabaseInstance{
		Name:            strings.TrimSpace(req.Name),
		Engine:          domain.DefaultDatabaseEngine(req.Engine),
		MachineID:       req.MachineID,
		Port:            req.Port,
		Username:        strings.TrimSpace(req.Username),
		DefaultDatabase: strings.TrimSpace(req.DefaultDatabase),
		Env:             domain.DefaultEnvironment(req.Env),
		Description:     req.Description,
		// New instances are enabled unless the caller says otherwise. The
		// default lives here, not in a gorm tag — see DatabaseInstance.Enabled.
		Enabled: req.Enabled == nil || *req.Enabled,
	}
	if inst.Port == 0 {
		inst.Port = 3306
	}
	if err := s.validate(ctx, inst); err != nil {
		return nil, err
	}
	if req.Password == "" {
		return nil, errors.New("password is required")
	}
	enc, err := s.encryptPassword(req.Password)
	if err != nil {
		return nil, err
	}
	inst.Password = enc

	if err := s.instances.Create(ctx, inst); err != nil {
		return nil, err
	}
	logStruct("database instance created", inst)
	return inst, nil
}

func (s *DatabaseInstanceService) Update(ctx context.Context, id int64, req UpdateDatabaseInstanceRequest) (*domain.DatabaseInstance, error) {
	inst, err := s.instances.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Name != "" {
		inst.Name = strings.TrimSpace(req.Name)
	}
	if req.Engine != "" {
		inst.Engine = req.Engine
	}
	if req.MachineID > 0 {
		inst.MachineID = req.MachineID
	}
	if req.Port > 0 {
		inst.Port = req.Port
	}
	if req.Username != "" {
		inst.Username = strings.TrimSpace(req.Username)
	}
	if req.DefaultDatabase != nil {
		inst.DefaultDatabase = strings.TrimSpace(*req.DefaultDatabase)
	}
	if req.Env != "" {
		inst.Env = req.Env
	}
	if req.Description != nil {
		inst.Description = *req.Description
	}
	if req.Enabled != nil {
		inst.Enabled = *req.Enabled
	}
	if err := s.validate(ctx, inst); err != nil {
		return nil, err
	}
	if req.Password != "" {
		enc, err := s.encryptPassword(req.Password)
		if err != nil {
			return nil, err
		}
		inst.Password = enc
	}
	if err := s.instances.Update(ctx, inst); err != nil {
		return nil, err
	}
	logStruct("database instance updated", inst)
	return inst, nil
}

func (s *DatabaseInstanceService) Get(ctx context.Context, id int64) (*domain.DatabaseInstance, error) {
	return s.instances.GetByID(ctx, id)
}

func (s *DatabaseInstanceService) List(ctx context.Context) ([]*domain.DatabaseInstance, error) {
	return s.instances.List(ctx)
}

func (s *DatabaseInstanceService) Delete(ctx context.Context, id int64) error {
	return s.instances.Delete(ctx, id)
}

// ResolvedInstance is everything a query needs: the instance itself, its
// password in plaintext, and the node endpoint to relay through. It never
// leaves the process.
type ResolvedInstance struct {
	Instance     *domain.DatabaseInstance
	Password     string
	AgentBaseURL string
	AgentToken   string
}

// Resolve loads an instance, decrypts its password, and resolves the machine's
// node endpoint, rejecting anything that cannot serve a query.
func (s *DatabaseInstanceService) Resolve(ctx context.Context, id int64) (*ResolvedInstance, error) {
	inst, err := s.instances.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !inst.Enabled {
		return nil, errors.New("this database instance is disabled")
	}
	machine, err := s.machines.Get(ctx, inst.MachineID)
	if err != nil {
		return nil, err
	}
	if machine.Mode != domain.MachineModeAgent {
		return nil, ErrDatabaseInstanceNotAgent
	}
	if machine.AgentBaseURL == "" {
		return nil, errors.New("this database's machine has no agent_base_url configured")
	}
	password, err := s.box.Decrypt(inst.Password)
	if err != nil {
		return nil, err
	}
	return &ResolvedInstance{
		Instance:     inst,
		Password:     password,
		AgentBaseURL: machine.AgentBaseURL,
		AgentToken:   machine.AgentToken,
	}, nil
}

// validate enforces what must hold for a registration to be usable, including
// the agent-mode requirement — caught at registration time so the operator
// finds out while filling the form, not on their first query.
func (s *DatabaseInstanceService) validate(ctx context.Context, inst *domain.DatabaseInstance) error {
	if inst.Name == "" {
		return errors.New("name is required")
	}
	if !inst.Engine.Valid() {
		return fmt.Errorf("unsupported engine %q (only mysql is available)", inst.Engine)
	}
	if !inst.Env.Valid() {
		return fmt.Errorf("invalid env %q", inst.Env)
	}
	if inst.Port <= 0 || inst.Port > 65535 {
		return errors.New("port must be a valid TCP port")
	}
	if inst.Username == "" {
		return errors.New("username is required")
	}
	if inst.MachineID <= 0 {
		return errors.New("machine_id is required")
	}
	machine, err := s.machines.Get(ctx, inst.MachineID)
	if err != nil {
		return err
	}
	if machine.Mode != domain.MachineModeAgent {
		return ErrDatabaseInstanceNotAgent
	}
	return nil
}

// encryptPassword mirrors MachineService.encryptAgentToken, including the
// warning when there is no master key to encrypt with — a database password in
// plaintext at rest deserves the same notice an agent token gets.
func (s *DatabaseInstanceService) encryptPassword(password string) (string, error) {
	if !s.box.Enabled() {
		logger.L().Warn("storing database instance password without encryption; set security.master_key to encrypt it at rest")
	}
	return s.box.Encrypt(password)
}
