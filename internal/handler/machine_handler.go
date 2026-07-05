package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

func (s *Server) listMachines(c *gin.Context) {
	machines, err := s.machineSvc.List(c.Request.Context())
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": machines, "total": len(machines)})
}

func (s *Server) createMachine(c *gin.Context) {
	var req service.CreateMachineRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	m, err := s.machineSvc.Create(c.Request.Context(), req)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Created(c, m)
}

func (s *Server) getMachine(c *gin.Context) {
	id, ok := paramInt64(c, "machineID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid machine ID")
		return
	}
	m, err := s.machineSvc.Get(c.Request.Context(), id)
	if err != nil {
		FailWith(c, http.StatusNotFound, err)
		return
	}
	Success(c, m)
}

func (s *Server) updateMachine(c *gin.Context) {
	id, ok := paramInt64(c, "machineID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid machine ID")
		return
	}
	var req service.UpdateMachineRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	m, err := s.machineSvc.Update(c.Request.Context(), id, req)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, m)
}

func (s *Server) deleteMachine(c *gin.Context) {
	id, ok := paramInt64(c, "machineID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid machine ID")
		return
	}
	if err := s.machineSvc.Delete(c.Request.Context(), id); err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	NoContent(c)
}

func (s *Server) testMachineConnection(c *gin.Context) {
	id, ok := paramInt64(c, "machineID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid machine ID")
		return
	}
	if err := s.machineSvc.TestConnection(c.Request.Context(), id); err != nil {
		Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	Success(c, gin.H{"ok": true})
}
