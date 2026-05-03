package agentwatcher

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type AlertSink interface {
	Send(alert Alert) error
}

type Handler struct {
	Config   Config
	Detector *Detector
	Sink     AlertSink
	Secrets  map[string]string
	Dedupe   *DedupeStore
	Now      func() time.Time
	Async    bool
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if err := h.verify(r, body); err != nil {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	deliveryID := deliveryID(r, body)
	if h.Dedupe != nil && !h.Dedupe.Seen(deliveryID, h.now()) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	event, err := NormalizeLinearWebhook(body)
	if err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if event.DeliveryID == "" {
		event.DeliveryID = deliveryID
	}
	alerts := h.Detector.Evaluate(event)
	if h.Async {
		go h.sendAlerts(append([]Alert(nil), alerts...))
	} else {
		h.sendAlerts(alerts)
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"alerts": len(alerts)})
}

func (h Handler) sendAlerts(alerts []Alert) {
	for _, alert := range alerts {
		if h.Sink != nil {
			_ = h.Sink.Send(alert)
		}
	}
}

func (h Handler) verify(r *http.Request, body []byte) error {
	secret := h.Secrets[h.Config.Webhook.SigningSecretEnv]
	if strings.TrimSpace(secret) == "" {
		return errors.New("missing signing secret")
	}
	signature := strings.TrimSpace(r.Header.Get("Linear-Signature"))
	if signature == "" {
		return errors.New("missing signature")
	}
	sum := hmac.New(sha256.New, []byte(secret))
	_, _ = sum.Write(body)
	expected := hex.EncodeToString(sum.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return errors.New("signature mismatch")
	}
	if err := verifyTimestamp(body, h.Config.TimestampTolerance(), h.now()); err != nil {
		return err
	}
	return nil
}

func verifyTimestamp(body []byte, tolerance time.Duration, now time.Time) error {
	var payload struct {
		WebhookTimestamp linearTimestamp `json:"webhookTimestamp"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	if payload.WebhookTimestamp.IsZero() {
		return nil
	}
	if payload.WebhookTimestamp.After(now.Add(tolerance)) || payload.WebhookTimestamp.Before(now.Add(-tolerance)) {
		return fmt.Errorf("stale webhook timestamp")
	}
	return nil
}

func deliveryID(r *http.Request, body []byte) string {
	if value := strings.TrimSpace(r.Header.Get("Linear-Delivery")); value != "" {
		return value
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (h Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

type DedupeStore struct {
	ttl    time.Duration
	mu     sync.Mutex
	values map[string]time.Time
	path   string
}

func NewDedupeStore(ttl time.Duration) *DedupeStore {
	return &DedupeStore{ttl: ttl, values: map[string]time.Time{}}
}

func (s *DedupeStore) Seen(key string, now time.Time) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for item, seenAt := range s.values {
		if seenAt.Add(s.ttl).Before(now) {
			delete(s.values, item)
		}
	}
	if _, ok := s.values[key]; ok {
		return false
	}
	s.values[key] = now
	_ = s.persistLocked(now)
	return true
}
