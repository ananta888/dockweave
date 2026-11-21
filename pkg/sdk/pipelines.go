package sdk

import (
	"context"
	"net/url"

	"github.com/ananta888/dockweave/internal/model"
)

// PutPipelines replaces the daemon's pipeline edges with the edges derived
// from graph revision rev (admin). reset accepts a revision lower than the
// stored one (graph document recreated).
func (c *Client) PutPipelines(ctx context.Context, rev int, reset bool, edges []model.PipelineEdge) (*model.PipelineState, error) {
	if edges == nil {
		edges = []model.PipelineEdge{}
	}
	var out model.PipelineState
	err := c.do(ctx, "PUT", "/api/v1/pipelines", map[string]any{"revision": rev, "reset": reset, "edges": edges}, &out)
	return &out, err
}

// ListPipelines returns the stored edges; agent (id or name) filters by
// downstream agent when non-empty.
func (c *Client) ListPipelines(ctx context.Context, agent string) (*model.PipelineState, error) {
	path := "/api/v1/pipelines"
	if agent != "" {
		path += "?agent=" + url.QueryEscape(agent)
	}
	var out model.PipelineState
	err := c.do(ctx, "GET", path, nil, &out)
	return &out, err
}

// PipelineAdmission asks whether the agent may take pipeline work now. It
// never consumes overrides.
func (c *Client) PipelineAdmission(ctx context.Context, agent string) (*model.Admission, error) {
	var out model.Admission
	err := c.do(ctx, "GET", "/api/v1/pipelines/admission?agent="+url.QueryEscape(agent), nil, &out)
	return &out, err
}

// PipelineAdmissions returns the admission of every agent that has edges.
func (c *Client) PipelineAdmissions(ctx context.Context) (int, []*model.Admission, error) {
	var out struct {
		Revision   int                `json:"revision"`
		Admissions []*model.Admission `json:"admissions"`
	}
	err := c.do(ctx, "GET", "/api/v1/pipelines/admission", nil, &out)
	return out.Revision, out.Admissions, err
}

// OverrideRequest creates an audited pipeline override (admin).
type OverrideRequest struct {
	Agent     string `json:"agent"`
	GateID    string `json:"gate_id"`
	Reason    string `json:"reason"`
	TTLSec    int    `json:"ttl_sec,omitempty"`
	SingleUse *bool  `json:"single_use,omitempty"`
}

// CreatePipelineOverride records an override for one blocked edge.
func (c *Client) CreatePipelineOverride(ctx context.Context, req OverrideRequest) (*model.PipelineOverride, error) {
	var out model.PipelineOverride
	err := c.do(ctx, "POST", "/api/v1/pipelines/override", req, &out)
	return &out, err
}

// ListPipelineOverrides lists overrides (agent empty = all; active only
// returns overrides that can still be used).
func (c *Client) ListPipelineOverrides(ctx context.Context, agent string, active bool) ([]*model.PipelineOverride, error) {
	q := url.Values{}
	if agent != "" {
		q.Set("agent", agent)
	}
	if active {
		q.Set("active", "true")
	}
	path := "/api/v1/pipelines/overrides"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		Overrides []*model.PipelineOverride `json:"overrides"`
	}
	err := c.do(ctx, "GET", path, nil, &out)
	return out.Overrides, err
}
