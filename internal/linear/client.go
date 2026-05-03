package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultEndpoint = "https://api.linear.app/graphql"

type Client struct {
	endpoint   string
	token      string
	httpClient *http.Client
}

type Option func(*Client)

func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		client.httpClient = httpClient
	}
}

func NewClient(endpoint, token string, opts ...Option) (*Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("linear token is required")
	}

	client := &Client{
		endpoint: endpoint,
		token:    token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(client)
	}
	if client.httpClient == nil {
		return nil, errors.New("http client is required")
	}
	return client, nil
}

func (c *Client) Do(ctx context.Context, query string, variables any, out any) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return errors.New("linear graphql query is required")
	}
	payload := graphQLRequest{
		Query:     query,
		Variables: variables,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode graphql request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build graphql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("linear graphql request failed: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read linear graphql response: %w", err)
	}
	var envelope graphQLResponse
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if err := json.Unmarshal(responseBody, &envelope); err == nil && len(envelope.Errors) > 0 {
			return GraphQLErrors(envelope.Errors)
		}
		return HTTPError{
			StatusCode: resp.StatusCode,
			RateLimit: RateLimitHeaders{
				Limit:     resp.Header.Get("X-RateLimit-Limit"),
				Remaining: resp.Header.Get("X-RateLimit-Remaining"),
				Reset:     resp.Header.Get("X-RateLimit-Reset"),
			},
		}
	}

	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return fmt.Errorf("decode linear graphql response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return GraphQLErrors(envelope.Errors)
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 {
		return errors.New("linear graphql response missing data")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode linear graphql data: %w", err)
	}
	return nil
}

type graphQLRequest struct {
	Query     string `json:"query"`
	Variables any    `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []GraphQLError  `json:"errors"`
}

type GraphQLError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

func (e GraphQLError) Code() string {
	if e.Extensions == nil {
		return ""
	}
	code, _ := e.Extensions["code"].(string)
	return code
}

type GraphQLErrors []GraphQLError

func (errs GraphQLErrors) Error() string {
	if len(errs) == 0 {
		return "linear graphql errors"
	}
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		if code := err.Code(); code != "" {
			parts = append(parts, fmt.Sprintf("%s (%s)", err.Message, code))
			continue
		}
		parts = append(parts, err.Message)
	}
	return "linear graphql errors: " + strings.Join(parts, "; ")
}

type HTTPError struct {
	StatusCode int
	RateLimit  RateLimitHeaders
}

func (e HTTPError) Error() string {
	if e.RateLimit.Remaining != "" || e.RateLimit.Reset != "" {
		return fmt.Sprintf("linear graphql http status %d (rate remaining=%s reset=%s)", e.StatusCode, e.RateLimit.Remaining, e.RateLimit.Reset)
	}
	return fmt.Sprintf("linear graphql http status %d", e.StatusCode)
}

type RateLimitHeaders struct {
	Limit     string
	Remaining string
	Reset     string
}
