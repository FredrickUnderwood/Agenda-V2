package service

import (
	"context"
	"errors"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/redisguard"
	"github.com/FredrickUnderwood/agenda-v2/internal/secret"
)

func baseRedisCreateRequest() CreateDatabaseInstanceRequest {
	return CreateDatabaseInstanceRequest{
		Name:      "cache-test",
		Engine:    domain.DatabaseEngineRedis,
		MachineID: 1,
		Username:  "",
		Password:  "",
		Env:       domain.EnvironmentTest,
	}
}

// A loopback-bound Redis commonly has neither an ACL user nor a requirepass,
// and demanding either would only make an operator invent one the server never
// checks. MySQL keeps both requirements.
func TestRegisterRedisWithoutCredentials(t *testing.T) {
	instSvc, _, _, _ := newDatabaseTestServices(t, secret.NewBox("master-key"))
	created, err := instSvc.Create(context.Background(), baseRedisCreateRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Port != 6379 {
		t.Fatalf("port = %d, want the Redis default 6379", created.Port)
	}

	mysqlReq := baseCreateRequest()
	mysqlReq.Name = "orders-nopass"
	mysqlReq.Password = ""
	if _, err := instSvc.Create(context.Background(), mysqlReq); err == nil {
		t.Fatal("a MySQL registration without a password should still be refused")
	}
}

func TestRegisterRedisRejectsANonNumericDefaultDatabase(t *testing.T) {
	instSvc, _, _, _ := newDatabaseTestServices(t, secret.NewBox(""))
	req := baseRedisCreateRequest()
	req.DefaultDatabase = "orders"
	if _, err := instSvc.Create(context.Background(), req); err == nil {
		t.Fatal("a Redis default database must be a numeric index")
	}

	req.DefaultDatabase = "3"
	created, err := instSvc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("create with a numeric default: %v", err)
	}
	if got := defaultRedisDB(created); got != 3 {
		t.Fatalf("default db = %d, want 3", got)
	}
}

// The two consoles must not be able to talk to each other's engine: a SELECT
// relayed to Redis would come back as an unreadable protocol error, and the
// operator would have no idea why.
func TestEnginesDoNotCrossOver(t *testing.T) {
	instSvc, querySvc, _, _ := newDatabaseTestServices(t, secret.NewBox(""))
	ctx := context.Background()
	admin := Principal{UserID: 1, Username: "root", IsAdmin: true}

	redisInst, err := instSvc.Create(ctx, baseRedisCreateRequest())
	if err != nil {
		t.Fatalf("create redis: %v", err)
	}
	mysqlInst, err := instSvc.Create(ctx, baseCreateRequest())
	if err != nil {
		t.Fatalf("create mysql: %v", err)
	}

	if _, err := querySvc.Query(ctx, admin, redisInst.ID, QueryRequest{SQL: "SELECT 1"}); !errors.Is(err, ErrWrongEngine) {
		t.Fatalf("SQL against a Redis instance: err = %v, want ErrWrongEngine", err)
	}
	if _, err := querySvc.ListDatabases(ctx, admin, redisInst.ID); !errors.Is(err, ErrWrongEngine) {
		t.Fatalf("SHOW DATABASES against a Redis instance: err = %v, want ErrWrongEngine", err)
	}
	if _, err := querySvc.RunRedisCommand(ctx, admin, mysqlInst.ID, RedisCommandRequest{Command: "PING"}); !errors.Is(err, ErrWrongEngine) {
		t.Fatalf("a command against a MySQL instance: err = %v, want ErrWrongEngine", err)
	}
}

// A refused command must not reach the node — it would travel with the Redis
// password attached — and must not be audited, because it never ran.
func TestRefusedCommandIsNotRelayedOrAudited(t *testing.T) {
	instSvc, querySvc, db, _ := newDatabaseTestServices(t, secret.NewBox(""))
	ctx := context.Background()
	inst, err := instSvc.Create(ctx, baseRedisCreateRequest())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = querySvc.RunRedisCommand(ctx, Principal{UserID: 1, Username: "root", IsAdmin: true}, inst.ID,
		RedisCommandRequest{Command: "FLUSHALL"})
	if !errors.Is(err, redisguard.ErrRejected) {
		t.Fatalf("err = %v, want a redisguard rejection", err)
	}

	var audited int64
	if err := db.Model(&domain.DBQueryLog{}).Count(&audited).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if audited != 0 {
		t.Fatalf("%d audit rows written for a command that never ran", audited)
	}
}

// The environment rule is one function for both engines; this is what keeps a
// second copy of it from appearing on the Redis path.
func TestRedisCommandHonorsTheEnvironmentRule(t *testing.T) {
	instSvc, querySvc, _, _ := newDatabaseTestServices(t, secret.NewBox(""))
	ctx := context.Background()
	req := baseRedisCreateRequest()
	req.Name = "cache-prod"
	req.Env = domain.EnvironmentProd
	inst, err := instSvc.Create(ctx, req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	member := Principal{UserID: 2, Username: "dev"}
	if _, err := querySvc.RunRedisCommand(ctx, member, inst.ID, RedisCommandRequest{Command: "GET k"}); !errors.Is(err, ErrQueryForbidden) {
		t.Fatalf("err = %v, want ErrQueryForbidden", err)
	}
}

func TestRedisVersionFromInfo(t *testing.T) {
	const info = "# Server\r\nredis_version:7.2.4\r\nredis_mode:standalone\r\n"
	if got := redisVersionFromInfo(info); got != "7.2.4" {
		t.Fatalf("version = %q, want 7.2.4", got)
	}
	if got := redisVersionFromInfo("# Server\r\nredis_mode:standalone\r\n"); got != "" {
		t.Fatalf("version = %q, want empty when INFO does not carry one", got)
	}
}
