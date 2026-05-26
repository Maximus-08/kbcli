# Agentic Interoperability Guidelines (`AGENTS.md`)

This guide is designed for AI agents and automated scripts that want to read from, write to, or manage the personal knowledge base using the `kb` tool.

---

## 1. Vault Directory Structure
The knowledge base follows a strict Obsidian-compatible layout:
- `sources/raw/`: The ingestion drop zone. Raw `.md` files captured from the web (e.g., via web clippers) or draft notes belong here.
- `wiki/`: The retrieval/compilation layer containing structured, synthesized markdown articles.
- `wiki/INDEX.md`: A structured, alphabetical, flat index of all articles in the wiki, used for retrieval-augmented context injection.

---

## 2. Recommended Agent Workflows

### A. Dropping New Information
When you discover new facts, extract notes, or capture pages:
1. Write a clean markdown file into `sources/raw/`.
2. Ensure the frontmatter is minimal but contains at least a descriptive `title` and status `uncompiled` (optional, since default is uncompiled).
   ```markdown
   ---
   title: "Neural Networks Concept"
   status: uncompiled
   ---
   Raw findings go here...
   ```

### B. Triggering Compilations
To compile new raw notes into structured wiki pages:
- **Run compilation:**
  ```bash
  kb compile --all
  ```
- **Force re-compilation (e.g., if raw content was updated):**
  ```bash
  kb compile --force sources/raw/note.md
  ```
- **Synthesizing multiple files together:**
  ```bash
  kb compile --multi sources/raw/part1.md sources/raw/part2.md
  ```
- **Deep-compile via Hub-and-Spoke splitting:**
  ```bash
  kb compile --split sources/raw/massive-note.md
  ```
- **Technical Compaction:**
  To optimize wiki articles and compress them losslessly using LLMs:
  ```bash
  kb compact slug-name
  # Or sweep-compact all wiki articles
  kb compact --all
  ```


### C. Contextual Querying & Reasoning (RAG)
To ask questions and gather synthesized knowledge directly from the wiki:
- **Run search queries:**
  ```bash
  kb query "Explain the key differences between RNNs and Transformers."
  ```
- This command will automatically:
  1. Scan `wiki/INDEX.md`.
  2. Use the LLM to select relevant wiki articles.
  3. Context-inject the selected article bodies into a synthesis prompt.
  4. Generate a cohesive markdown response with inline citations (e.g., `[[transformer-architecture]]`).

### D. Maintaining Integrity and Linting
Before finishing a reasoning step or committing updates to the vault:
- **Run the linter:**
  ```bash
  kb lint
  ```
- **Fix index discrepancies:**
  If you manually edit, add, or delete wiki files, the index might become stale. Run:
  ```bash
  kb rebuild-index
  ```
- **Clean up orphans and redundants:**
  To detect and remove redundant or orphaned articles:
  ```bash
  kb cleanup --dry-run
  # Or force cleanup automatically
  kb cleanup --force
  ```

---

## 3. CLI Exit Code Reference

Always check the exit code of `kb` commands to handle failures programmatically:
* `0`: Success / No errors.
* `1`: General execution error (e.g., compilation failed, linter found diagnostics with severity `ERROR`).
* `2`: Configuration or Environment Error (e.g., missing `.env`, invalid paths, missing variables).
* `3`: Model unreachable (e.g., local Ollama instance is down, Groq rate-limited with no fallback).
