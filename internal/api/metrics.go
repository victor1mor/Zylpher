package api

import (
	"fmt"
	"net/http"
)

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	metrics, err := s.webhook.Metrics(r.Context())
	if err != nil {
		s.log.Warn("failed to read webhook metrics", "error", err)
		writeError(w, http.StatusServiceUnavailable, "metrics_unavailable", "could not read webhook metrics")
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `# HELP zylpher_webhook_pending Events awaiting acknowledgement for the configured receiver.
# TYPE zylpher_webhook_pending gauge
zylpher_webhook_pending %d
# HELP zylpher_webhook_delivery_failures_total Failed delivery attempts in this process.
# TYPE zylpher_webhook_delivery_failures_total counter
zylpher_webhook_delivery_failures_total %d
# HELP zylpher_webhook_retries_total Retry attempts started in this process.
# TYPE zylpher_webhook_retries_total counter
zylpher_webhook_retries_total %d
# HELP zylpher_webhook_oldest_pending_age_seconds Age of the oldest queued event in seconds.
# TYPE zylpher_webhook_oldest_pending_age_seconds gauge
zylpher_webhook_oldest_pending_age_seconds %.3f
`, metrics.Pending, metrics.DeliveryFailures, metrics.Retries, metrics.OldestAgeSeconds)
}
