package llm

import (
	"context"
	"iter"
)

// RoutingClient delegates Stream calls to the client registered for the
// requested model. Models not in the route table fall through to the
// default client. ListModels merges default + routed models.
type RoutingClient struct {
	defaultClient Client
	routes        map[string]Client // model ID → client
}

// NewRoutingClient builds a RoutingClient. routes maps model IDs to the
// Client that should handle them. defaultClient handles anything not in
// the map.
func NewRoutingClient(defaultClient Client, routes map[string]Client) *RoutingClient {
	return &RoutingClient{
		defaultClient: defaultClient,
		routes:        routes,
	}
}

func (r *RoutingClient) Stream(ctx context.Context, req Request) (iter.Seq[Event], error) {
	c, ok := r.routes[req.Model]
	if !ok {
		c = r.defaultClient
	}
	return c.Stream(ctx, req)
}

func (r *RoutingClient) ListModels(ctx context.Context) ([]Model, error) {
	defaultModels, err := r.defaultClient.ListModels(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(defaultModels))
	for _, m := range defaultModels {
		seen[m.ID] = true
	}

	models := make([]Model, 0, len(defaultModels)+len(r.routes))
	models = append(models, defaultModels...)

	for id := range r.routes {
		if !seen[id] {
			models = append(models, Model{ID: id})
			seen[id] = true
		}
	}

	return models, nil
}
