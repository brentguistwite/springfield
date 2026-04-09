package execution

// Config is Springfield's product-facing execution config projection.
type Config struct {
	PlansDir                   string
	WorktreeBase               string
	MaxRetries                 int
	SingleWorkstreamIterations int
	SingleWorkstreamTimeout    int
	Tool                       string
	FallbackTool               string
	Batches                    [][]string
	Sequential                 []string
}

// Input holds Springfield-managed execution setup inputs.
type Input struct {
	PlansDir                   string
	WorktreeBase               string
	MaxRetries                 int
	SingleWorkstreamIterations int
	SingleWorkstreamTimeout    int
}

// Result describes the outcome of creating execution config.
type Result struct {
	Created bool
	Reused  bool
	Path    string
}

// UpdateResult describes the outcome of updating execution config.
type UpdateResult struct {
	Updated bool
	Path    string
}
