package githubbackstop

import (
	"context"
	"strings"
	"testing"
)

func TestOwnerResolverResolvesSingleOwnerLabel(t *testing.T) {
	resolver := OwnerResolver{Client: fakeLinearOwnerClient{response: ownerIssueResponse{
		Issue: struct {
			Identifier string "json:\"identifier\""
			Labels     struct {
				Nodes []struct {
					Name string "json:\"name\""
				} "json:\"nodes\""
			} "json:\"labels\""
		}{
			Identifier: "HAD-665",
			Labels: struct {
				Nodes []struct {
					Name string "json:\"name\""
				} "json:\"nodes\""
			}{
				Nodes: []struct {
					Name string "json:\"name\""
				}{
					{Name: "owner:denovo"},
					{Name: "bug"},
				},
			},
		},
	}}}
	resolution, err := resolver.ResolveOwnerLabel(context.Background(), "had-665")
	if err != nil {
		t.Fatalf("ResolveOwnerLabel() error = %v", err)
	}
	if resolution.IssueKey != "HAD-665" || resolution.OwnerLabel != "owner:denovo" || resolution.ConflictReason != "" {
		t.Fatalf("resolution = %#v", resolution)
	}
}

func TestOwnerResolverReportsOwnerConflicts(t *testing.T) {
	resolver := OwnerResolver{Client: fakeLinearOwnerClient{response: ownerIssueResponse{
		Issue: ownerIssue("HAD-665", "owner:hermes", "owner:denovo"),
	}}}
	resolution, err := resolver.ResolveOwnerLabel(context.Background(), "HAD-665")
	if err != nil {
		t.Fatalf("ResolveOwnerLabel() error = %v", err)
	}
	if resolution.OwnerLabel != "" || resolution.ConflictReason != "owner_label_conflict" {
		t.Fatalf("resolution = %#v", resolution)
	}
}

func TestOwnerResolverReportsMissingOwnerLabel(t *testing.T) {
	resolver := OwnerResolver{Client: fakeLinearOwnerClient{response: ownerIssueResponse{
		Issue: ownerIssue("HAD-665", "bug", "customer"),
	}}}
	resolution, err := resolver.ResolveOwnerLabel(context.Background(), "HAD-665")
	if err != nil {
		t.Fatalf("ResolveOwnerLabel() error = %v", err)
	}
	if resolution.OwnerLabel != "" || resolution.ConflictReason != ReasonOwnerLabelMissing {
		t.Fatalf("resolution = %#v, want missing owner label", resolution)
	}
}

func TestOwnerResolverRequiresIdentifier(t *testing.T) {
	resolver := OwnerResolver{Client: fakeLinearOwnerClient{response: ownerIssueResponse{
		Issue: ownerIssue("", "owner:denovo"),
	}}}
	_, err := resolver.ResolveOwnerLabel(context.Background(), "HAD-665")
	if err == nil || !strings.Contains(err.Error(), "missing identifier") {
		t.Fatalf("ResolveOwnerLabel() error = %v, want missing identifier", err)
	}
}

type fakeLinearOwnerClient struct {
	response ownerIssueResponse
	err      error
}

func (f fakeLinearOwnerClient) Do(_ context.Context, _ string, _ any, out any) error {
	if f.err != nil {
		return f.err
	}
	*out.(*ownerIssueResponse) = f.response
	return nil
}

func ownerIssue(identifier string, labelNames ...string) struct {
	Identifier string "json:\"identifier\""
	Labels     struct {
		Nodes []struct {
			Name string "json:\"name\""
		} "json:\"nodes\""
	} "json:\"labels\""
} {
	issue := struct {
		Identifier string "json:\"identifier\""
		Labels     struct {
			Nodes []struct {
				Name string "json:\"name\""
			} "json:\"nodes\""
		} "json:\"labels\""
	}{
		Identifier: identifier,
	}
	for _, name := range labelNames {
		issue.Labels.Nodes = append(issue.Labels.Nodes, struct {
			Name string "json:\"name\""
		}{Name: name})
	}
	return issue
}
