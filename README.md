# KB System — Personal Knowledge Base Pipeline

`kb` is a unified, single-binary Go CLI tool designed to compile, lint, watch, cleanup, and query a personal Obsidian-compatible markdown knowledge base.

---

## Features

- **Single Binary (`kb`)**: Equipped with subcommands powered by Cobra.
- **Vault-Aware**: Automatically detects your vault root from your current working directory (cwd) or uses `.env` configuration.
- **Atomic Compilations**: Compiles raw dropped documents from a drop zone into organized, clean wiki articles with automatically generated slugs, tag extraction, and frontmatter.
- **Background Watcher**: Monitors the raw drop zone for incoming files with debouncing, sequential compilation queuing, and fallback polling mode for WSL/network mounts.
- **10 Diagnostic Lint Checks**: Ensures the health and consistency of frontmatter metadata, page references, dead links, unindexed pages, and stale sources.
- **Automated Cleanup**: Detects orphans and redundancy (using Jaccard Title heuristics + LLM overlap checks) and safely trashes files to the Obsidian `.trash/` folder.
- **Intelligent Querying (RAG)**: Asks natural language questions over the entire vault. Uses LLM-driven indexing selection to inject context and generates syntheses with inline wiki citations.

---

## Project Layout

```
x:\home\avnis\dev\projects\knowledgebase\
├── kb-system/                          ← Go Project Root
│   ├── cmd/
│   │   └── kb/
│   │       └── main.go                 ← Entry Point
│   ├── internal/
│   │   ├── cli/                        ← CLI Subcommands (root, compile, lint, watch, query, rebuild-index, cleanup)
│   │   ├── config/                     ← Configuration loader
│   │   ├── compiler/                   ← Compilation orchestrator
│   │   ├── frontmatter/                ← Frontmatter YAML parsers
│   │   ├── index/                      ← Thread-safe INDEX.md manager
│   │   ├── provider/                   ← LLM API Clients (Ollama, Groq, Fallback)
│   │   ├── querier/                    ← RAG Query flow
│   │   └── watcher/                    ← Filesystem watch pipeline
│   └── prompts/                        ← Embedded default prompt templates
│
└── vault-kb/                           ← Obsidian Vault
    ├── sources/
    │   └── raw/                        ← Raw Ingestion drop zone
    └── wiki/
        ├── INDEX.md                    ← Retrieval Index Layer
        └── *.md                        ← Compiled Wiki Articles
```

---

## Installation & Setup

### 1. Prerequisites
- **Go**: Version 1.22 or higher
- **Ollama**: Local instance running (e.g., model `gemma4:e4b` or similar)

### 2. Configuration
Create a `.env` file in the `kb-system/` directory (see `.env.example`):
```env
VAULT_KB_PATH=../vault-kb
COMPILE_MODEL_SINGLE=gemma4:e4b
COMPILE_MODEL_MULTI=gemma4:e4b
LINT_MODEL=gemma4:e4b
QUERY_MODEL=gemma4:e4b
CLEANUP_MODEL=gemma4:e4b
OLLAMA_BASE_URL=http://localhost:11434
LOG_LEVEL=info
```

### 3. Build & Run
To compile the single binary locally:
```powershell
powershell -ExecutionPolicy Bypass -File .\build.ps1
```
The compiled executable will be written to `.\bin\kb.exe`.

---

## Subcommand Reference

### 1. Ingestion / Compilation
Compile raw sources in `sources/raw/` into organized `wiki/` articles:
```bash
# Compile specific files
.\bin\kb.exe compile sources/raw/note.md

# Compile all uncompiled raw notes
.\bin\kb.exe compile --all

# Synthesize multiple files into a single article
.\bin\kb.exe compile --multi sources/raw/part1.md sources/raw/part2.md
```

### 2. Pipeline Watcher
Watch the raw drop zone in the background to automatically compile incoming notes:
```bash
.\bin\kb.exe watch

# Use polling fallback mode (useful for WSL network mount file events)
.\bin\kb.exe watch --poll
```

### 3. Consistency Linting
Ensure the integrity and sanity of your knowledge base:
```bash
.\bin\kb.exe lint
```

### 4. Smart Cleanup
Detect and remove orphaned or highly redundant wiki files:
```bash
# Run a dry-run check
.\bin\kb.exe cleanup --dry-run

# Move orphans/redundant articles to .trash/
.\bin\kb.exe cleanup --force
```

### 5. Natural Language Querying (RAG)
Ask natural language questions across the compiled wiki:
```bash
.\bin\kb.exe query "Summarize what we have on deep learning transformers"
```

### 6. Index Regeneration
Recover or rebuild `wiki/INDEX.md` directly from frontmatter metadata:
```bash
.\bin\kb.exe rebuild-index
```
