package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// sdTarget is one entry of Prometheus's http_sd_configs discovery response:
// https://prometheus.io/docs/prometheus/latest/configuration/configuration/#http_sd_config
// All entries share the control plane's own host:port as __address__ (set via
// relabel_configs in the operator's prometheus.yml, not here) — the real
// per-target identity travels in Labels and gets rewritten into the "target"
// query param scrapeAppMetrics reads, the same "multi-target exporter"
// pattern blackbox_exporter uses.
type sdTarget struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// listScrapeTargets is Prometheus's http_sd_configs discovery endpoint,
// enumerating every metrics-enabled application instance. Prometheus requires
// a bare top-level JSON array, so this calls Success with a slice directly
// rather than the gin.H{"data":...} envelope other list endpoints use.
func (s *Server) listScrapeTargets(c *gin.Context) {
	targets, err := s.appMetricsSvc.ListScrapeTargets(c.Request.Context())
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]sdTarget, 0, len(targets))
	for _, t := range targets {
		id := strconv.FormatInt(t.TargetID, 10)
		out = append(out, sdTarget{
			Targets: []string{id},
			Labels: map[string]string{
				"job":                     "agenda-app-metrics",
				"__meta_agenda_app":       t.AppName,
				"__meta_agenda_env":       t.Env,
				"__meta_agenda_instance":  t.InstanceName,
				"__meta_agenda_target_id": id,
			},
		})
	}
	Success(c, out)
}

// scrapeAppMetrics is the metrics_path Prometheus's agenda-app-metrics job
// scrapes for each discovered target, relaying the raw exposition-format
// body from that target's agenda-node verbatim.
func (s *Server) scrapeAppMetrics(c *gin.Context) {
	targetID, err := strconv.ParseInt(c.Query("target"), 10, 64)
	if err != nil || targetID <= 0 {
		c.String(http.StatusBadRequest, "target query param must be a valid id")
		return
	}
	body, contentType, err := s.appMetricsSvc.FetchInstanceMetrics(c.Request.Context(), targetID)
	if err != nil {
		c.String(http.StatusBadGateway, "%s", err.Error())
		return
	}
	if contentType == "" {
		contentType = "text/plain; version=0.0.4; charset=utf-8"
	}
	c.Data(http.StatusOK, contentType, body)
}
