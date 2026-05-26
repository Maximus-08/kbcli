package prompts

import (
	_ "embed"
)

//go:embed compile_single.txt
var CompileSingle string

//go:embed compile_multi.txt
var CompileMulti string

//go:embed query_select.txt
var QuerySelect string

//go:embed query_answer.txt
var QueryAnswer string

//go:embed cleanup_redundancy.txt
var CleanupRedundancy string

//go:embed lint_check.txt
var LintCheck string

//go:embed split_plan.txt
var SplitPlan string

//go:embed compile_spoke.txt
var CompileSpoke string

//go:embed compile_hub.txt
var CompileHub string

//go:embed ingest_analysis.txt
var IngestAnalysis string

//go:embed compile_expand.txt
var CompileExpand string

//go:embed compact.txt
var Compact string

//go:embed synthesize_split_plan.txt
var SynthesizeSplitPlan string

//go:embed compile_hub_synthesis.txt
var CompileHubSynthesis string


