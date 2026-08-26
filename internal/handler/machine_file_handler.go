package handler

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/filestore"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

// maxUploadFormBytes bounds the whole multipart request. It is enforced here
// rather than relying on the node's own cap so an oversized body is rejected
// before it is streamed across the network to a machine that will refuse it.
const maxUploadFormBytes = 256 << 20

// openerFor adapts a multipart file header into a service.FileOpener. The
// header can be opened repeatedly, which is what lets an environment upload
// stream the same content to several machines without buffering it in memory.
func openerFor(fh *multipart.FileHeader) service.FileOpener {
	return func() (io.ReadCloser, error) { return fh.Open() }
}

// uploadFileForm is the shared shape of both upload endpoints' form fields.
type uploadFileForm struct {
	file      *multipart.FileHeader
	mode      string
	overwrite bool
	// field returns any other form value. Callers take their values from here
	// rather than from the gin context, because reading one parses the whole
	// body — which must not happen before the size limit is installed.
	field func(string) string
}

// readUploadForm installs the request size limit and then parses the multipart
// body. Order matters: reading any form value parses the body, so a limit
// applied afterwards would already have been bypassed.
func (s *Server) readUploadForm(c *gin.Context) (uploadFileForm, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadFormBytes)
	fh, err := c.FormFile("file")
	if err != nil {
		FailMessage(c, http.StatusBadRequest, "a 'file' part is required: "+err.Error())
		return uploadFileForm{}, false
	}
	return uploadFileForm{
		file:      fh,
		mode:      c.PostForm("mode"),
		overwrite: c.PostForm("overwrite") == "true",
		field:     c.PostForm,
	}, true
}

// uploadApplicationEnvFile delivers one file to every machine hosting the
// application environment, under the directory that is bind-mounted into the
// containers.
func (s *Server) uploadApplicationEnvFile(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	form, ok := s.readUploadForm(c)
	if !ok {
		return
	}
	env := domain.Environment(form.field("env"))
	name := form.field("file_name")
	if name == "" {
		name = form.file.Filename
	}

	results, err := s.machineFileSvc.UploadToAppEnv(
		c.Request.Context(), s.principal(c), appID, env, name, form.mode, form.overwrite, openerFor(form.file))
	if err != nil {
		FailWith(c, machineFileStatus(err), err)
		return
	}
	// A partial success is still a 200 with per-machine detail: the caller has
	// to see which machines took the file, and an error status would suggest
	// that none of them did.
	Success(c, gin.H{
		"data":           results,
		"container_path": service.ContainerPath(name),
	})
}

func (s *Server) listApplicationEnvFiles(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	items, err := s.machineFileSvc.ListForAppEnv(c.Request.Context(), appID, domain.Environment(c.Query("env")))
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": items, "total": len(items)})
}

// uploadMachineFile writes one file to an operator-chosen path on one machine.
func (s *Server) uploadMachineFile(c *gin.Context) {
	machineID, ok := paramInt64(c, "machineID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid machine ID")
		return
	}
	form, ok := s.readUploadForm(c)
	if !ok {
		return
	}
	targetPath := form.field("path")
	if targetPath == "" {
		FailMessage(c, http.StatusBadRequest, "'path' is required (absolute path on the machine)")
		return
	}

	rec, err := s.machineFileSvc.UploadToMachine(
		c.Request.Context(), s.principal(c), machineID, targetPath, form.mode, form.overwrite, openerFor(form.file))
	if err != nil {
		FailWith(c, machineFileStatus(err), err)
		return
	}
	Created(c, rec)
}

func (s *Server) listMachineFiles(c *gin.Context) {
	machineID, ok := paramInt64(c, "machineID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid machine ID")
		return
	}
	items, err := s.machineFileSvc.ListForMachine(c.Request.Context(), machineID)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": items, "total": len(items)})
}

// verifyMachineFile re-reads one recorded file on its machine and returns the
// updated record. This is what the console's per-row check button calls.
func (s *Server) verifyMachineFile(c *gin.Context) {
	id, ok := paramInt64(c, "fileID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid file ID")
		return
	}
	rec, err := s.machineFileSvc.Verify(c.Request.Context(), id)
	if err != nil {
		FailWith(c, machineFileStatus(err), err)
		return
	}
	Success(c, rec)
}

func machineFileStatus(err error) int {
	switch {
	case errors.Is(err, service.ErrMachineFileNotFound):
		return http.StatusNotFound
	case errors.Is(err, filestore.ErrExists):
		return http.StatusConflict
	case errors.Is(err, filestore.ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, service.ErrNoMachinesForEnv),
		errors.Is(err, filestore.ErrPathInvalid),
		errors.Is(err, filestore.ErrOutsideRoots),
		errors.Is(err, filestore.ErrIsDir):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
