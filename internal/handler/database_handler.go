package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/redisguard"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
	"github.com/FredrickUnderwood/agenda-v2/internal/sqlguard"
)

// principal flattens the request identity for the service layer. When
// authentication is not configured the API is open (dev mode), and the caller
// is treated as an admin — the same behavior requireAdmin already has, so the
// two gates cannot disagree about what dev mode means.
func (s *Server) principal(c *gin.Context) service.Principal {
	if !s.auth.Enabled() {
		return service.Principal{Username: "dev", IsAdmin: true}
	}
	id, ok := auth.GetIdentity(c)
	if !ok {
		return service.Principal{}
	}
	return service.Principal{UserID: id.UserID, Username: id.Username, IsAdmin: id.Has(auth.PermAll)}
}

func (s *Server) listDatabaseInstances(c *gin.Context) {
	items, err := s.dbInstanceSvc.List(c.Request.Context())
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": items, "total": len(items)})
}

func (s *Server) getDatabaseInstance(c *gin.Context) {
	id, ok := paramInt64(c, "instanceID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid database instance ID")
		return
	}
	inst, err := s.dbInstanceSvc.Get(c.Request.Context(), id)
	if err != nil {
		FailWith(c, http.StatusNotFound, err)
		return
	}
	Success(c, inst)
}

func (s *Server) createDatabaseInstance(c *gin.Context) {
	var req service.CreateDatabaseInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	inst, err := s.dbInstanceSvc.Create(c.Request.Context(), req)
	if err != nil {
		FailWith(c, databaseInstanceStatus(err), err)
		return
	}
	Created(c, inst)
}

func (s *Server) updateDatabaseInstance(c *gin.Context) {
	id, ok := paramInt64(c, "instanceID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid database instance ID")
		return
	}
	var req service.UpdateDatabaseInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	inst, err := s.dbInstanceSvc.Update(c.Request.Context(), id, req)
	if err != nil {
		FailWith(c, databaseInstanceStatus(err), err)
		return
	}
	Success(c, inst)
}

func (s *Server) deleteDatabaseInstance(c *gin.Context) {
	id, ok := paramInt64(c, "instanceID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid database instance ID")
		return
	}
	if err := s.dbInstanceSvc.Delete(c.Request.Context(), id); err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	NoContent(c)
}

// testDatabaseInstance reports connectivity the way testMachineConnection does:
// a failure to connect is a 200 carrying ok:false, because "the credentials are
// wrong" is a successful answer to the question the operator asked.
func (s *Server) testDatabaseInstance(c *gin.Context) {
	id, ok := paramInt64(c, "instanceID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid database instance ID")
		return
	}
	version, err := s.dbQuerySvc.TestInstance(c.Request.Context(), id)
	if err != nil {
		Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	Success(c, gin.H{"ok": true, "server_version": version})
}

func (s *Server) queryDatabaseInstance(c *gin.Context) {
	id, ok := paramInt64(c, "instanceID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid database instance ID")
		return
	}
	var req service.QueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.dbQuerySvc.Query(c.Request.Context(), s.principal(c), id, req)
	if err != nil {
		FailWith(c, queryStatus(err), err)
		return
	}
	Success(c, result)
}

// runRedisCommand is queryDatabaseInstance's Redis counterpart. It is a
// separate route rather than a branch inside the query route because the two
// carry different bodies — a statement and a schema versus a command and a
// numeric DB index — and folding them together would mean guessing which one
// the caller meant.
func (s *Server) runRedisCommand(c *gin.Context) {
	id, ok := paramInt64(c, "instanceID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid database instance ID")
		return
	}
	var req service.RedisCommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.dbQuerySvc.RunRedisCommand(c.Request.Context(), s.principal(c), id, req)
	if err != nil {
		FailWith(c, queryStatus(err), err)
		return
	}
	Success(c, result)
}

// getRedisDatabases backs the console's DB picker: how many numeric databases
// this server has, so the picker offers the right range instead of assuming 16.
func (s *Server) getRedisDatabases(c *gin.Context) {
	id, ok := paramInt64(c, "instanceID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid database instance ID")
		return
	}
	count, err := s.dbQuerySvc.RedisDatabaseCount(c.Request.Context(), s.principal(c), id)
	if err != nil {
		FailWith(c, queryStatus(err), err)
		return
	}
	Success(c, gin.H{"count": count})
}

func (s *Server) listDatabaseInstanceDatabases(c *gin.Context) {
	id, ok := paramInt64(c, "instanceID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid database instance ID")
		return
	}
	names, err := s.dbQuerySvc.ListDatabases(c.Request.Context(), s.principal(c), id)
	if err != nil {
		FailWith(c, queryStatus(err), err)
		return
	}
	Success(c, gin.H{"data": names, "total": len(names)})
}

func (s *Server) listDatabaseInstanceTables(c *gin.Context) {
	id, ok := paramInt64(c, "instanceID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid database instance ID")
		return
	}
	names, err := s.dbQuerySvc.ListTables(c.Request.Context(), s.principal(c), id, c.Query("database"))
	if err != nil {
		FailWith(c, queryStatus(err), err)
		return
	}
	Success(c, gin.H{"data": names, "total": len(names)})
}

// getDatabaseInstanceSchema backs the editor's completion: one call returns
// every table in a schema with its columns, rather than making the browser ask
// per table.
func (s *Server) getDatabaseInstanceSchema(c *gin.Context) {
	id, ok := paramInt64(c, "instanceID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid database instance ID")
		return
	}
	outline, err := s.dbQuerySvc.SchemaOutline(c.Request.Context(), s.principal(c), id, c.Query("database"))
	if err != nil {
		FailWith(c, queryStatus(err), err)
		return
	}
	Success(c, gin.H{"tables": outline})
}

func (s *Server) listDBQueryLogs(c *gin.Context) {
	limit := queryInt(c, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	instanceID := int64(queryInt(c, "instance_id", 0)) // 0 = every instance
	items, total, err := s.dbQuerySvc.ListLogs(c.Request.Context(), s.principal(c), instanceID, limit, queryInt(c, "offset", 0))
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": items, "total": total})
}

func (s *Server) getDBQueryLog(c *gin.Context) {
	id, ok := paramInt64(c, "logID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid query log ID")
		return
	}
	entry, err := s.dbQuerySvc.GetLog(c.Request.Context(), s.principal(c), id)
	if err != nil {
		FailWith(c, http.StatusNotFound, err)
		return
	}
	Success(c, entry)
}

// queryStatus maps a query failure to the status that describes it. Anything
// either guard rejected — and anything aimed at the wrong engine — is the
// caller's statement (400); a permission failure is
// 403; an instance bound to a non-agent machine is a configuration conflict
// (409); everything else is treated as the database or its node being
// unreachable (502), which is what a bad password or a stopped server looks
// like from here.
func queryStatus(err error) int {
	switch {
	case errors.Is(err, sqlguard.ErrRejected), errors.Is(err, redisguard.ErrRejected):
		return http.StatusBadRequest
	case errors.Is(err, service.ErrWrongEngine):
		return http.StatusBadRequest
	case errors.Is(err, service.ErrQueryForbidden):
		return http.StatusForbidden
	case errors.Is(err, service.ErrDatabaseInstanceNotFound):
		return http.StatusNotFound
	case errors.Is(err, service.ErrDatabaseInstanceNotAgent):
		return http.StatusConflict
	default:
		return http.StatusBadGateway
	}
}

func databaseInstanceStatus(err error) int {
	if errors.Is(err, service.ErrDatabaseInstanceNotAgent) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}
