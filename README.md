# KB System — Personal Knowledge Base Pipeline

`kb` is a unified, high-performance, single-binary Go CLI tool designed to compile, watch, lint, compact, cleanup, and query a personal Obsidian-compatible markdown knowledge base. By integrating local LLMs and advanced heuristics, it automates the transition from raw web clippers/drafts to a highly structured, semantically queryable personal wiki.

---

## 🚀 Key Features

* **Single-Binary CLI (`kb`)**: Built with Go and powered by Cobra, offering a comprehensive suite of developer-friendly subcommands.
* **Smart Vault Autodetection**: Automatically discovers your vault root from your current working directory (Cwd) or falls back to `.env` configuration.
* **Atomic Compilations**: Compiles raw notes in a drop zone into organized, clean wiki articles with automatic slugification, frontmatter generation, and tag extraction.
* **Deep Split Compilation (`--split` / `-s`)**: Parses complex or massive notes and dynamically splits them into atomic, highly cohesive sub-topic articles linked through a hub-and-spoke structure.
* **Technical Compaction (`compact`)**: Employs lossless tech-compaction on wiki articles to maximize semantic density and eliminate redundant phrasing.
* **Robust File Watcher**: Monitors the ingestion drop zone in the background with sequential debounced compilation queuing, featuring a polling fallback mode (`--poll`) for WSL or network shares where filesystem events are inconsistent.
* **12 Comprehensive Lint Diagnostics**: Scans your entire vault to detect formatting errors, broken internal references, dangling indices, stale compiled sources, and low information density.
* **Automated Link Resolution (`lint --fix`)**: Automatically repairs case mismatches and resolves internal wiki references to their correct slugs.
* **Intelligent Redundancy Cleanup**: Employs Jaccard Title heuristics and LLM semantic checks to detect redundant or orphaned articles, safely trashing them to the Obsidian `.trash/` directory.
* **RAG-Driven Semantic Querying**: Pose natural language questions over the entire vault. Uses LLM-based indexing selection to inject optimal context, generating synthesized answers complete with inline wiki citations (e.g., `[[deep-learning]]`).

---

## 📁 Project Architecture

The Go binary resides and operates directly in the project root, managing your designated Obsidian vault path:

```
x:\home\avnis\dev\projects\knowledgebase\    ← Go Project Root
├── cmd/
│   └── kb/
│       └── main.go                         ← CLI Entrypoint
├── internal/
│   ├── cleaner/                            ← Redundancy & orphan cleanup manager
│   ├── cli/                                ← CLI command handlers (root, compile, watch, lint, cleanup, query, compact, rebuild-index)
│   ├── compiler/                           ← Ingestion compilation orchestrator (single, multi, split)
│   ├── config/                             ← Environment and .env configuration loader
│   ├── frontmatter/                        ← Strict YAML frontmatter parsers & marshals
│   ├── index/                              ← Thread-safe INDEX.md manager
│   ├── linter/                             ← Consistency check linter & auto-link fixer
│   ├── provider/                           ← Multi-endpoint LLM chain (Gemini, OpenRouter, Groq, Ollama)
│   ├── querier/                            ← Retrieval-Augmented Generation (RAG) query engine
│   ├── vault/                              ← Vault directory management & link resolution utilities
│   └── watcher/                            ← FSNotify / Polling filesystem event watcher
├── prompts/                                ← Embedded LLM prompt templates (.txt)
├── bin/                                    ← Output directory for built executables
└── build.ps1                               ← PowerShell build utility to bypass network share lock constraints
```

Your Obsidian vault folder matches this standard structure:

```
your-obsidian-vault/                        ← Obsidian Vault Directory
├── sources/
│   └── raw/                                ← Raw Ingestion Drop Zone (.md drafts & web clips)
└── wiki/
    ├── INDEX.md                            ← Auto-generated search & RAG Index Layer
    └── *.md                                ← Compiled, clean wiki articles
```

---

## 🛠️ Installation & Setup

### 1. Prerequisites
* **Go**: Version 1.22 or higher (1.24+ recommended)
* **Local LLM (optional)**: Ollama running locally (default models: `gemma4:e4b`, `llama-4-scout`, or equivalents)
* **Cloud Providers (optional)**: Gemini API, Groq, or OpenRouter for accelerated cloud synthesis

### 2. Configuration
Create a `.env` file in the project root directory (refer to [.env.example](file:///x:/home/avnis/dev/projects/knowledgebase/.env.example)):

```env
VAULT_KB_PATH=C:\Users\avnis\Desktop\QuantumAI
COMPILE_MODEL_SINGLE=gemma-4-31b-it
COMPILE_MODEL_MULTI=gemma-4-31b-it
LINT_MODEL=gemini-3.1-flash-lite
QUERY_MODEL=gemini-3.5-flash
CLEANUP_MODEL=gemini-3.1-flash-lite

OLLAMA_BASE_URL=http://localhost:11434
LOG_LEVEL=info

# API Keys
GEMINI_API_KEY=your_gemini_key
OPENROUTER_API_KEY=your_openrouter_key
```

### 3. Compilation
To build the optimized single-binary executable:
```powershell
powershell -ExecutionPolicy Bypass -File .\build.ps1
```
The compiled binary will be placed at `.\bin\kb.exe`.

---

## 💻 CLI Subcommand Reference

### 1. Ingestion / Compilation (`compile`)
Processes raw source files from the ingestion drop zone and compiles them into clean, structured wiki articles.

```bash
# Compile a specific raw file
.\bin\kb.exe compile sources/raw/note.md

# Compile all uncompiled notes in the raw directory
.\bin\kb.exe compile --all

# Synthesize multiple raw files together into a single structured wiki article
.\bin\kb.exe compile --multi sources/raw/part1.md sources/raw/part2.md

# Deep-compile by dynamically splitting large documents into atomic Spoke articles linked via a central Hub
.\bin\kb.exe compile --split sources/raw/massive-deep-dive.md

# List files that would be compiled without running the compilation
.\bin\kb.exe compile --all --dry-run
```

**Compilation Options:**
* `-a, --all`: Compile all uncompiled sources.
* `-f, --force`: Force compilation even if the file is already marked as compiled.
* `-m, --multi`: Combine multiple raw inputs into one synthesized wiki file.
* `-s, --split`: Split a massive note into highly cohesive hub-and-spoke sub-topics.
* `--dry-run`: Dry-run simulation mode.

---

### 2. Technical Compaction (`compact`)
Applies technical compression techniques using your designated LLM to maximize the informational density of your articles without losing any key insights.

```bash
# Compact a specific wiki article using its slug
.\bin\kb.exe compact quantum-enhanced-contrastive-learning

# Sweep and compact all wiki articles in the vault
.\bin\kb.exe compact --all
```

**Compaction Options:**
* `--all`: Sweeps and applies lossless technical compaction to all wiki articles.

---

### 3. Pipeline Watcher (`watch`)
Runs a background daemon that monitors the raw drop zone for newly added or updated draft notes and compiles them instantly.

```bash
# Start the watcher daemon
.\bin\kb.exe watch

# Run in polling mode (highly recommended for WSL mounts or SMB network drives)
.\bin\kb.exe watch --poll
```

**Watcher Options:**
* `--poll`: Bypasses fsnotify and uses safety-fallback directory polling.

---

### 4. Integrity Linting (`lint`)
Validates the consistency, structure, and link integrity of your entire knowledge base.

```bash
# Perform standard vault integrity check
.\bin\kb.exe lint

# Run linter and automatically fix broken case formats or mismatched wikilinks
.\bin\kb.exe lint --fix
```

#### Diagnostic Codes Reference

The linter evaluates articles against 12 strict rules:

| Code | Severity | Target Area | Diagnostic Rule Description |
| :--- | :--- | :--- | :--- |
| **`L000`** | `ERROR` | Core Structure | File read failure or malformed YAML frontmatter syntax. |
| **`L001`** | `ERROR` | Frontmatter | Missing critical `title` frontmatter field. |
| **`L002`** | `ERROR` | Frontmatter | Missing or invalid `type` (must be `concept`, `project`, `resource`, `synthesis`, or `query-result`). |
| **`L003`** | `WARNING`| Frontmatter | Invalid tag count (must be between 2 and 5 tags to ensure searchability). |
| **`L004`** | `ERROR` | Frontmatter | Missing or invalid `created` / `updated` dates (must match `YYYY-MM-DD`). |
| **`L005`** | `ERROR` | Frontmatter | Missing or invalid `provenance` metadata (must match `extracted`, `inferred`, `ambiguous`, `synthesis`). |
| **`L006`** | `WARNING`| Frontmatter | Missing or empty `summary` description. |
| **`L007`** | `WARNING`| Indexing | Wiki article exists but is not registered in `wiki/INDEX.md`. |
| **`L008`** | `ERROR` | Indexing | **Dangling entry**: File listed in `INDEX.md` is missing from `wiki/` (ERROR), or summary in index mismatch (WARNING). |
| **`L009`** | `ERROR` | Links | **Dead Wikilink**: Double-bracket link `[[slug]]` refers to a non-existent wiki article. |
| **`L010`** | `WARNING`| Sources | **Stale Source**: Raw note marked `compiled` but is not referenced in any wiki article. |
| **`L011`** | `WARNING`| Information | **Low density**: Contains conversational filler (e.g. "in summary", "in conclusion") or verbose prose (>6 sentences/paragraph). |

---

### 5. Smart Cleanup (`cleanup`)
Identifies wiki articles that are orphaned or exhibit severe overlap/redundancy, then safely removes them.

```bash
# Scan and report potential redundant or orphaned wiki files
.\bin\kb.exe cleanup --dry-run

# Automatically move redundant or orphaned files directly to Obsidian .trash/
.\bin\kb.exe cleanup --force
```

**Cleanup Options:**
* `--dry-run`: Scans and reports files without moving them.
* `-f, --force`: Moves files to Obsidian `.trash/` immediately without prompt confirmations.

---

### 6. Semantic Querying (`query`)
Runs localized semantic retrieval-augmented generation (RAG) directly against your personal wiki.

```bash
# Query your compiled knowledge
.\bin\kb.exe query "What are the primary differences between QCNNs and MPSQCL?"
```

---

### 7. Index Rebuilding (`rebuild-index`)
Overwrites or repairs a damaged `wiki/INDEX.md` file, scanning all wiki files to generate a structured, alphabetical, flat index of all articles.

```bash
.\bin\kb.exe rebuild-index
```

---

## 🚦 CLI Exit Codes

Automated scripts and AI agents can check the CLI exit codes to handle execution states programmatically:

* `0`: Success.
* `1`: General execution failure (e.g., compilation failed, linter found diagnostics with severity `ERROR`).
* `2`: Configuration or Environment Error (e.g., missing `.env`, invalid vault path, invalid Ollama URL).
* `3`: Model unreachable (e.g., Ollama offline, rate-limits on Groq/Gemini with no available fallbacks).
