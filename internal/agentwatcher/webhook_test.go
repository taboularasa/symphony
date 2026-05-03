package agentwatcher

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandlerRejectsBadSignature(t *testing.T) {
	handler := testHandler(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/linear", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Linear-Signature", "bad")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandlerRejectsStaleTimestamp(t *testing.T) {
	handler := testHandler(t, nil)
	body := []byte(`{"webhookTimestamp":"2026-05-03T20:00:00Z"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/linear", bytes.NewReader(body))
	req.Header.Set("Linear-Signature", signBody("secret", body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandlerDedupesDelivery(t *testing.T) {
	sink := &recordingSink{}
	handler := testHandler(t, sink)
	body := []byte(`{
		"webhookTimestamp":"2026-05-03T21:00:00Z",
		"type":"Comment",
		"actor":{"id":"user-hermes","email":"hermes-bot@hadto.net"},
		"data":{"issue":{"id":"issue-1","identifier":"HAD-651","project":{"name":"Symphony"}}}
	}`)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/linear", bytes.NewReader(body))
		req.Header.Set("Linear-Delivery", "delivery-1")
		req.Header.Set("Linear-Signature", signBody("secret", body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status %d = %d", i, rec.Code)
		}
	}
	if len(sink.alerts) != 1 {
		t.Fatalf("alerts = %+v", sink.alerts)
	}
}

func TestHandlerDedupesByBodyHashWhenDeliveryMissing(t *testing.T) {
	sink := &recordingSink{}
	handler := testHandler(t, sink)
	body := []byte(`{
		"webhookTimestamp":"2026-05-03T21:00:00Z",
		"type":"Comment",
		"actor":{"id":"user-hermes","email":"hermes-bot@hadto.net"},
		"data":{"issue":{"id":"issue-1","identifier":"HAD-651","project":{"name":"Symphony"}}}
	}`)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/linear", bytes.NewReader(body))
		req.Header.Set("Linear-Signature", signBody("secret", body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status %d = %d", i, rec.Code)
		}
	}
	if len(sink.alerts) != 1 {
		t.Fatalf("alerts = %+v", sink.alerts)
	}
}

func testHandler(t *testing.T, sink AlertSink) Handler {
	t.Helper()
	cfg := mustConfig(t)
	detector := NewDetector(cfg)
	now := time.Date(2026, 5, 3, 21, 0, 0, 0, time.UTC)
	detector.clock = func() time.Time { return now }
	return Handler{
		Config:   cfg,
		Detector: detector,
		Sink:     sink,
		Secrets:  map[string]string{"LINEAR_WEBHOOK_SECRET": "secret"},
		Dedupe:   NewDedupeStore(5 * time.Minute),
		Now:      func() time.Time { return now },
	}
}

func signBody(secret string, body []byte) string {
	sum := hmac.New(sha256.New, []byte(secret))
	_, _ = sum.Write(body)
	return hex.EncodeToString(sum.Sum(nil))
}

type recordingSink struct {
	alerts []Alert
}

func (s *recordingSink) Send(alert Alert) error {
	s.alerts = append(s.alerts, alert)
	return nil
}
