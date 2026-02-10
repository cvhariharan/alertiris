package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/dgraph-io/badger/v4"
)

type AlertmanagerPayload struct {
	Receiver          string            `json:"receiver"`
	Status            string            `json:"status"`
	Alerts            []Alert           `json:"alerts"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
}

type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

type Handler struct {
	iris   *IRISClient
	db     *badger.DB
	config AlertConfig
}

func NewHandler(iris *IRISClient, db *badger.DB, config AlertConfig) *Handler {
	return &Handler{iris: iris, db: db, config: config}
}

func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload AlertmanagerPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		slog.Error("failed to decode payload", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	for _, alert := range payload.Alerts {
		if err := h.processAlert(alert); err != nil {
			slog.Error("failed to process alert", "fingerprint", alert.Fingerprint, "error", err)
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) processAlert(alert Alert) error {
	fp := alert.Fingerprint
	existingID, err := h.getAlertID(fp)
	if err != nil && err != badger.ErrKeyNotFound {
		return fmt.Errorf("db lookup: %w", err)
	}
	exists := err == nil

	switch alert.Status {
	case "firing":
		if exists {
			return h.updateAlert(existingID, alert)
		}
		return h.createAlert(alert)
	case "resolved":
		if !exists {
			slog.Warn("resolved alert not found in db, skipping", "fingerprint", fp)
			return nil
		}
		return h.resolveAlert(existingID, alert)
	default:
		slog.Warn("unknown alert status", "status", alert.Status, "fingerprint", fp)
		return nil
	}
}

func (h *Handler) createAlert(alert Alert) error {
	sourceContent, _ := json.Marshal(alert)

	req := IRISAlertRequest{
		Title:            alert.Labels["alertname"],
		Description:      alertDescription(alert),
		Source:           h.config.Source,
		SourceRef:        alert.Fingerprint,
		SourceLink:       alert.GeneratorURL,
		SourceEventTime:  alert.StartsAt,
		SourceContent:    json.RawMessage(sourceContent),
		SeverityID:       h.severityID(alert),
		StatusID:         h.config.StatusIDNew,
		CustomerID:       h.config.CustomerID,
		Tags:             alert.Labels["alertname"],
	}

	alertID, err := h.iris.CreateAlert(req)
	if err != nil {
		return fmt.Errorf("create iris alert: %w", err)
	}

	if err := h.storeAlertID(alert.Fingerprint, alertID); err != nil {
		return fmt.Errorf("store alert mapping: %w", err)
	}

	slog.Info("created iris alert", "fingerprint", alert.Fingerprint, "alert_id", alertID)
	return nil
}

func (h *Handler) updateAlert(alertID int, alert Alert) error {
	sourceContent, _ := json.Marshal(alert)
	desc := alertDescription(alert)
	sevID := h.severityID(alert)
	tags := alert.Labels["alertname"]

	req := IRISAlertUpdateRequest{
		Description:     &desc,
		SourceEventTime: &alert.StartsAt,
		SourceContent:   json.RawMessage(sourceContent),
		SeverityID:      &sevID,
		Tags:            &tags,
	}

	if err := h.iris.UpdateAlert(alertID, req); err != nil {
		return fmt.Errorf("update iris alert %d: %w", alertID, err)
	}

	slog.Info("updated iris alert", "fingerprint", alert.Fingerprint, "alert_id", alertID)
	return nil
}

func (h *Handler) resolveAlert(alertID int, alert Alert) error {
	if h.config.ResolvedAction == "delete" {
		if err := h.iris.DeleteAlert(alertID); err != nil {
			return fmt.Errorf("delete iris alert %d: %w", alertID, err)
		}
		slog.Info("deleted iris alert", "fingerprint", alert.Fingerprint, "alert_id", alertID)
	} else {
		statusID := h.config.StatusIDResolved
		req := IRISAlertUpdateRequest{
			StatusID: &statusID,
		}
		if err := h.iris.UpdateAlert(alertID, req); err != nil {
			return fmt.Errorf("resolve iris alert %d: %w", alertID, err)
		}
		slog.Info("resolved iris alert", "fingerprint", alert.Fingerprint, "alert_id", alertID)
	}

	if err := h.deleteAlertID(alert.Fingerprint); err != nil {
		return fmt.Errorf("delete alert mapping: %w", err)
	}
	return nil
}

func (h *Handler) severityID(alert Alert) int {
	if sev, ok := alert.Labels["severity"]; ok {
		if id, ok := h.config.SeverityMap[sev]; ok {
			return id
		}
	}
	return h.config.DefaultSeverityID
}

func (h *Handler) getAlertID(fingerprint string) (int, error) {
	var alertID int
	err := h.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(dbKey(fingerprint))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			id, err := strconv.Atoi(string(val))
			if err != nil {
				return err
			}
			alertID = id
			return nil
		})
	})
	return alertID, err
}

func (h *Handler) storeAlertID(fingerprint string, alertID int) error {
	return h.db.Update(func(txn *badger.Txn) error {
		return txn.Set(dbKey(fingerprint), []byte(strconv.Itoa(alertID)))
	})
}

func (h *Handler) deleteAlertID(fingerprint string) error {
	return h.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(dbKey(fingerprint))
	})
}

func dbKey(fingerprint string) []byte {
	return []byte("fp:" + fingerprint)
}

func alertDescription(alert Alert) string {
	var lines []string

	add := func(key, val string) {
		if val != "" {
			lines = append(lines, key+": "+val)
		}
	}

	add("Alert", alert.Labels["alertname"])
	add("Severity", alert.Labels["severity"])
	add("Description", alert.Annotations["description"])
	add("Summary", alert.Annotations["summary"])
	add("Hostname", alert.Labels["hostname"])
	add("Instance", alert.Labels["instance_name"])
	add("Service", alert.Labels["service"])
	add("Group", alert.Labels["group"])
	add("Tier", alert.Labels["tier"])
	add("Load", alert.Annotations["load"])
	add("Started At", alert.StartsAt)
	add("Fingerprint", alert.Fingerprint)
	add("Generator URL", alert.GeneratorURL)

	return strings.Join(lines, "\n")
}
