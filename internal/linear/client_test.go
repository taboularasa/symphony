package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClientRequiresToken(t *testing.T) {
	if _, err := NewClient(DefaultEndpoint, ""); err == nil {
		t.Fatal("expected missing token error")
	}
}

func TestClientDoSetsAuthorizationAndDecodesData(t *testing.T) {
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		var req graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Query == "" {
			t.Fatal("missing query")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1"}}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "linear-key")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var out struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
	}
	if err := client.Do(context.Background(), "query { viewer { id } }", nil, &out); err != nil {
		t.Fatalf("do: %v", err)
	}
	if sawAuth != "linear-key" {
		t.Fatalf("authorization header = %q", sawAuth)
	}
	if out.Viewer.ID != "u1" {
		t.Fatalf("viewer id = %q", out.Viewer.ID)
	}
}

func TestClientDoReturnsGraphQLErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"rate limited","extensions":{"code":"RATELIMITED"}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "linear-key")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	var out map[string]any
	err = client.Do(context.Background(), "query { viewer { id } }", nil, &out)
	if err == nil {
		t.Fatal("expected graphql error")
	}
	if got, want := err.Error(), "linear graphql errors: rate limited (RATELIMITED)"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestClientDoReturnsGraphQLErrorsOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Rate limit exceeded","extensions":{"code":"RATELIMITED"}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "linear-key")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	err = client.Do(context.Background(), "query { issues { nodes { id } } }", nil, nil)
	if err == nil {
		t.Fatal("expected graphql error")
	}
	if _, ok := err.(GraphQLErrors); !ok {
		t.Fatalf("error type = %T", err)
	}
	if got, want := err.Error(), "linear graphql errors: Rate limit exceeded (RATELIMITED)"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestClientDoReturnsHTTPErrorWithRateLimitHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "42")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`too many requests`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "linear-key")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	err = client.Do(context.Background(), "query { viewer { id } }", nil, nil)
	if err == nil {
		t.Fatal("expected http error")
	}
	httpErr, ok := err.(HTTPError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d", httpErr.StatusCode)
	}
	if httpErr.RateLimit.Remaining != "0" || httpErr.RateLimit.Reset != "42" {
		t.Fatalf("rate headers = %+v", httpErr.RateLimit)
	}
}

func TestClientDoReturnsAuthHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`unauthorized`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "bad-key")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	err = client.Do(context.Background(), "query { viewer { id } }", nil, nil)
	if err == nil {
		t.Fatal("expected http error")
	}
	httpErr, ok := err.(HTTPError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if httpErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", httpErr.StatusCode)
	}
}

func TestClientDoRejectsMalformedGraphQLResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "linear-key")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	err = client.Do(context.Background(), "query { viewer { id } }", nil, nil)
	if err == nil {
		t.Fatal("expected decode error")
	}
}
