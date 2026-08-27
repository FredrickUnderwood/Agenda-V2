package node

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/filestore"
)

// putFile streams one uploaded file to an absolute path on this machine.
//
// The body is the file's raw bytes rather than a multipart form: this endpoint
// receives exactly one file, and a raw body streams straight to disk with no
// intermediate buffering, which is what keeps a large upload from being read
// into the node's memory.
//
// The path is re-validated here even though the control plane resolved it:
// this endpoint is reachable by anything holding the node token, so the
// file_roots confinement is only real if the node itself enforces it.
func (s *Server) putFile(c *gin.Context) {
	path, err := filestore.ValidatePath(c.Query(contract.NodeFileQueryPath), s.fileRoots)
	if err != nil {
		c.JSON(fileErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	mode, err := filestore.ParseMode(c.Query(contract.NodeFileQueryMode))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	overwrite := c.Query(contract.NodeFileQueryOverwrite) == "true"

	body := http.MaxBytesReader(c.Writer, c.Request.Body, s.maxUploadBytes+1)
	stat, err := filestore.WriteAt(path, mode, overwrite, body, s.maxUploadBytes)
	if err != nil {
		c.JSON(fileErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stat)
}

// statFile reports whether a file is still there and what its contents hash to.
//
// A missing file answers 200 with exists=false, not 404: "the file the control
// plane expects here is gone" is the answer to a verification request, and
// turning it into an error would make it indistinguishable from the node being
// unreachable — which is the one distinction the caller most needs.
func (s *Server) statFile(c *gin.Context) {
	path, err := filestore.ValidatePath(c.Query(contract.NodeFileQueryPath), s.fileRoots)
	if err != nil {
		c.JSON(fileErrorStatus(err), gin.H{"error": err.Error()})
		return
	}
	stat, err := filestore.StatAt(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stat)
}

func fileErrorStatus(err error) int {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, filestore.ErrPathInvalid), errors.Is(err, filestore.ErrIsDir):
		return http.StatusBadRequest
	case errors.Is(err, filestore.ErrOutsideRoots):
		return http.StatusForbidden
	case errors.Is(err, filestore.ErrExists):
		return http.StatusConflict
	case errors.Is(err, filestore.ErrTooLarge), errors.As(err, &tooLarge):
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusInternalServerError
	}
}
