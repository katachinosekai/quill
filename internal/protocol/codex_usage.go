package protocol

// CodexUsage captures both the accumulated thread totals and the latest
// context size. Cached input and reasoning are included in their totals.
type CodexUsage struct {
	Total              CodexTokenBreakdown `json:"total"`
	Last               CodexTokenBreakdown `json:"last"`
	ModelContextWindow int                 `json:"modelContextWindow"`
}

type CodexTokenBreakdown struct {
	Total           int64 `json:"totalTokens"`
	Input           int64 `json:"inputTokens"`
	CachedInput     int64 `json:"cachedInputTokens"`
	CacheWriteInput int64 `json:"cacheWriteInputTokens"`
	Output          int64 `json:"outputTokens"`
	ReasoningOutput int64 `json:"reasoningOutputTokens"`
}
