package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/secret"
)

// fakeMachineGetterDB stands in for MachineService.Get: registration only cares
// which mode the machine runs in.
type fakeMachineGetterDB struct{ machines map[int64]*domain.Machine }

func (f *fakeMachineGetterDB) Get(_ context.Context, id int64) (*domain.Machine, error) {
	m, ok := f.machines[id]
	if !ok {
		return nil, errors.New("machine not found")
	}
	return m, nil
}

type stubQuerySettings map[string]string

func (s stubQuerySettings) Get(key string) (string, bool) {
	v, ok := s[key]
	return v, ok
}

// Uses a real (in-memory) DB rather than fakes: what these tests check is which
// rows are written and which come back for a given caller, which a fake repo
// would only assert back at itself. It is also the only way to catch the GORM
// zero-value/default interaction below.
func newDatabaseTestServices(t *testing.T, box *secret.Box) (*DatabaseInstanceService, *DatabaseQueryService, *gorm.DB, *fakeMachineGetterDB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&domain.DatabaseInstance{}, &domain.DBQueryLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	machines := &fakeMachineGetterDB{machines: map[int64]*domain.Machine{
		1: {ID: 1, Mode: domain.MachineModeAgent, AgentBaseURL: "http://127.0.0.1:7100", AgentToken: "tok"},
		2: {ID: 2, Mode: domain.MachineModeSSH, Host: "10.0.0.1"},
	}}
	instSvc := NewDatabaseInstanceService(repository.NewDatabaseInstanceRepository(db), machines, box)
	querySvc := NewDatabaseQueryService(instSvc, repository.NewDBQueryLogRepository(db), stubQuerySettings{}, box)
	return instSvc, querySvc, db, machines
}

func baseCreateRequest() CreateDatabaseInstanceRequest {
	return CreateDatabaseInstanceRequest{
		Name:      "orders-prod",
		MachineID: 1,
		Port:      3306,
		Username:  "agenda_ro",
		Password:  "s3cret",
		Env:       domain.EnvironmentProd,
	}
}

func TestAuthorizeQuery(t *testing.T) {
	admin := Principal{UserID: 1, Username: "root", IsAdmin: true}
	member := Principal{UserID: 2, Username: "dev"}

	cases := []struct {
		name    string
		p       Principal
		env     domain.Environment
		allowed bool
	}{
		{"admin on prod", admin, domain.EnvironmentProd, true},
		{"admin on stage", admin, domain.EnvironmentStage, true},
		{"admin on test", admin, domain.EnvironmentTest, true},
		{"member on test", member, domain.EnvironmentTest, true},
		{"member on prod", member, domain.EnvironmentProd, false},
		{"member on stage", member, domain.EnvironmentStage, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := AuthorizeQuery(tc.p, &domain.DatabaseInstance{Env: tc.env})
			if tc.allowed && err != nil {
				t.Fatalf("expected access, got %v", err)
			}
			if !tc.allowed {
				if err == nil {
					t.Fatal("expected access to be refused")
				}
				if !errors.Is(err, ErrQueryForbidden) {
					t.Fatalf("error %v does not wrap ErrQueryForbidden", err)
				}
			}
		})
	}
}

func TestCreateRejectsSSHMachine(t *testing.T) {
	instSvc, _, _, _ := newDatabaseTestServices(t, secret.NewBox(""))
	req := baseCreateRequest()
	req.MachineID = 2 // ssh mode

	_, err := instSvc.Create(context.Background(), req)
	if !errors.Is(err, ErrDatabaseInstanceNotAgent) {
		t.Fatalf("err = %v, want ErrDatabaseInstanceNotAgent", err)
	}
}

// A bool with a gorm `default:true` tag is dropped from the INSERT when it is
// false, so the row comes back enabled — the trap that broke gateway health
// gating. DatabaseInstance.Enabled carries no default tag for that reason; this
// test is what keeps someone from "tidying" one back in.
func TestCreatePersistsExplicitlyDisabled(t *testing.T) {
	instSvc, _, db, _ := newDatabaseTestServices(t, secret.NewBox(""))
	disabled := false
	req := baseCreateRequest()
	req.Enabled = &disabled

	created, err := instSvc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var reloaded domain.DatabaseInstance
	if err := db.First(&reloaded, created.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Enabled {
		t.Fatal("instance created as disabled came back enabled; a gorm default tag is swallowing the false")
	}
}

func TestCreateDefaultsToEnabled(t *testing.T) {
	instSvc, _, db, _ := newDatabaseTestServices(t, secret.NewBox(""))
	created, err := instSvc.Create(context.Background(), baseCreateRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var reloaded domain.DatabaseInstance
	if err := db.First(&reloaded, created.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Enabled {
		t.Fatal("a new instance should be enabled")
	}
}

func TestPasswordIsEncryptedAtRestAndRecoveredForTheNode(t *testing.T) {
	instSvc, _, db, _ := newDatabaseTestServices(t, secret.NewBox("master-key"))
	created, err := instSvc.Create(context.Background(), baseCreateRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var stored domain.DatabaseInstance
	if err := db.First(&stored, created.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Password == "s3cret" {
		t.Fatal("password was written to the database in plaintext")
	}
	if !secret.IsEncrypted(stored.Password) {
		t.Fatalf("stored password %q is not an encrypted blob", stored.Password)
	}

	resolved, err := instSvc.Resolve(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Password != "s3cret" {
		t.Fatalf("resolved password = %q, want the plaintext", resolved.Password)
	}
	if resolved.AgentBaseURL != "http://127.0.0.1:7100" || resolved.AgentToken != "tok" {
		t.Fatalf("resolve did not carry the node endpoint: %+v", resolved)
	}
}

func TestUpdateWithoutPasswordKeepsTheStoredOne(t *testing.T) {
	instSvc, _, _, _ := newDatabaseTestServices(t, secret.NewBox("master-key"))
	created, err := instSvc.Create(context.Background(), baseCreateRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := instSvc.Update(context.Background(), created.ID, UpdateDatabaseInstanceRequest{Port: 3307}); err != nil {
		t.Fatalf("update: %v", err)
	}
	resolved, err := instSvc.Resolve(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Password != "s3cret" {
		t.Fatalf("password after an unrelated edit = %q, want it unchanged", resolved.Password)
	}
	if resolved.Instance.Port != 3307 {
		t.Fatalf("port = %d, want 3307", resolved.Instance.Port)
	}
}

func TestResolveRejectsDisabledInstance(t *testing.T) {
	instSvc, _, _, _ := newDatabaseTestServices(t, secret.NewBox(""))
	disabled := false
	req := baseCreateRequest()
	req.Enabled = &disabled
	created, err := instSvc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := instSvc.Resolve(context.Background(), created.ID); err == nil {
		t.Fatal("a disabled instance should not resolve")
	}
}

func seedLog(t *testing.T, db *gorm.DB, userID int64, username string, at time.Time) *domain.DBQueryLog {
	t.Helper()
	entry := &domain.DBQueryLog{
		InstanceID: 1, InstanceName: "orders-prod", Env: domain.EnvironmentProd,
		UserID: userID, Username: username, DatabaseName: "orders",
		Statement: "SELECT 1", Success: true, CreatedAt: at,
	}
	if err := db.Create(entry).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
	return entry
}

func TestListLogsShowsOnlyOwnEntriesToAMember(t *testing.T) {
	_, querySvc, db, _ := newDatabaseTestServices(t, secret.NewBox(""))
	now := time.Now()
	seedLog(t, db, 7, "alice", now)
	seedLog(t, db, 8, "bob", now)
	seedLog(t, db, 7, "alice", now)

	items, total, err := querySvc.ListLogs(context.Background(), Principal{UserID: 7, Username: "alice"}, 0, 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("member saw %d entries (total %d), want 2 — the narrowing must happen in the query", len(items), total)
	}
	for _, it := range items {
		if it.UserID != 7 {
			t.Fatalf("member was shown user %d's entry", it.UserID)
		}
	}
}

func TestListLogsShowsEveryEntryToAnAdmin(t *testing.T) {
	_, querySvc, db, _ := newDatabaseTestServices(t, secret.NewBox(""))
	now := time.Now()
	seedLog(t, db, 7, "alice", now)
	seedLog(t, db, 8, "bob", now)

	items, total, err := querySvc.ListLogs(context.Background(), Principal{UserID: 1, Username: "root", IsAdmin: true}, 0, 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("admin saw %d entries (total %d), want 2", len(items), total)
	}
}

func TestGetLogRefusesAnotherUsersEntryAsNotFound(t *testing.T) {
	_, querySvc, db, _ := newDatabaseTestServices(t, secret.NewBox(""))
	other := seedLog(t, db, 8, "bob", time.Now())

	_, err := querySvc.GetLog(context.Background(), Principal{UserID: 7, Username: "alice"}, other.ID)
	if !errors.Is(err, ErrAuditEntryNotFound) {
		// Distinguishing "exists but not yours" from "does not exist" would
		// itself leak that somebody ran a query.
		t.Fatalf("err = %v, want ErrAuditEntryNotFound", err)
	}

	if _, err := querySvc.GetLog(context.Background(), Principal{UserID: 1, IsAdmin: true}, other.ID); err != nil {
		t.Fatalf("admin should be able to read any entry, got %v", err)
	}
}

func TestPurgeExpiredLogsHonorsTheConfiguredWindow(t *testing.T) {
	_, querySvc, db, _ := newDatabaseTestServices(t, secret.NewBox(""))
	querySvc.settings = stubQuerySettings{SettingQueryLogRetentionDays: "7"}

	seedLog(t, db, 7, "alice", time.Now().AddDate(0, 0, -30))
	seedLog(t, db, 7, "alice", time.Now().AddDate(0, 0, -1))

	removed, err := querySvc.PurgeExpiredLogs(context.Background())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if removed != 1 {
		t.Fatalf("purged %d entries, want 1", removed)
	}
	var remaining int64
	db.Model(&domain.DBQueryLog{}).Count(&remaining)
	if remaining != 1 {
		t.Fatalf("%d entries left, want 1", remaining)
	}
}

func TestQueryRejectsAWriteBeforeItTouchesTheNode(t *testing.T) {
	instSvc, querySvc, _, _ := newDatabaseTestServices(t, secret.NewBox(""))
	created, err := instSvc.Create(context.Background(), baseCreateRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The agent base URL points at a port nothing is listening on, so if the
	// guard let this through the failure would be a connection error instead.
	_, err = querySvc.Query(context.Background(),
		Principal{UserID: 1, IsAdmin: true}, created.ID,
		QueryRequest{SQL: "DELETE FROM orders"})
	if err == nil {
		t.Fatal("a write statement should be refused")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err = %v, want the read-only guard's rejection", err)
	}
}

func TestQueryRefusedForMemberOnProdIsNotAudited(t *testing.T) {
	instSvc, querySvc, db, _ := newDatabaseTestServices(t, secret.NewBox(""))
	created, err := instSvc.Create(context.Background(), baseCreateRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = querySvc.Query(context.Background(),
		Principal{UserID: 9, Username: "mallory"}, created.ID,
		QueryRequest{SQL: "SELECT 1"})
	if !errors.Is(err, ErrQueryForbidden) {
		t.Fatalf("err = %v, want ErrQueryForbidden", err)
	}
	var count int64
	db.Model(&domain.DBQueryLog{}).Count(&count)
	if count != 0 {
		t.Fatalf("%d audit entries written; a query that was never run should not be recorded as one", count)
	}
}

func TestQueryOnAnUnknownInstanceIsNotFound(t *testing.T) {
	_, querySvc, _, _ := newDatabaseTestServices(t, secret.NewBox(""))

	_, err := querySvc.Query(context.Background(),
		Principal{UserID: 1, IsAdmin: true}, 4242,
		QueryRequest{SQL: "SELECT 1"})
	// Falling through to the generic path would report a typo in the URL as the
	// database being unreachable.
	if !errors.Is(err, ErrDatabaseInstanceNotFound) {
		t.Fatalf("err = %v, want ErrDatabaseInstanceNotFound", err)
	}
}
