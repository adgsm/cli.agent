# Embedding-based Semantic Search

## Goal

Enable the agent to semantically search the local codebase using Ollama embedding models, going beyond exact text matching (`find_files` + `grep`) to support queries like "find files related to authentication flow."

## Available Models

These models are already pulled locally:
- `nomic-embed-text-v2-moe:latest`
- `qwen3-embedding:0.6b`
- `qwen3-embedding:4b`
- `qwen3-embedding:8b`
- `qllama/multilingual-e5-large-instruct:latest`

## Architecture

### New Components

```
internal/
  embeddings/
    embedder.go       Calls Ollama /api/embeddings to generate vectors
    store.go          Local vector store (SQLite + sqlite-vec or flat JSON)
    indexer.go        Walks directories, chunks files, generates embeddings
  tools/
    search.go         semantic_search tool: embed query, find top-k results
```

### Tool Interface

```
semantic_search(query: string, top_k?: int, dir?: string) -> ranked file list with snippets
```

### Data Flow

```
User asks "find authentication code"
  ↓
Agent calls semantic_search("authentication flow")
  ↓
Tool embeds query via Ollama /api/embeddings
  ↓
Similarity search against stored vectors
  ↓
Returns top-k file paths with matching snippets
  ↓
Agent reads relevant files with read_file
```

### Indexing Pipeline

1. **On-demand indexing** — when `semantic_search` is first called, index the working directory
2. **Chunking** — split files into ~500-token chunks with overlap
3. **Storage** — store chunks + embeddings + metadata (file path, line range) in a local DB
4. **Incremental updates** — re-index only changed files (track by mtime + hash)
5. **Index location** — `~/.cache/cli-agent/index/<hash-of-dir-path>/`

### Ollama Integration

Call `POST /api/embeddings` with each chunk:
```json
{
  "model": "nomic-embed-text-v2-moe",
  "prompt": "file chunk text here"
}
```

Returns: `{"embedding": [0.1, 0.2, ...]}`

### Vector Storage Options

| Option | Pros | Cons |
|--------|------|------|
| **Flat JSON files** | Zero deps, simple | Slow for large codebases, no incremental |
| **SQLite + sqlite-vec** | Fast, queryable, single file | CGo dependency |
| **SQLite + brute-force** | No CGo, simple SQL | Slower for 100k+ vectors |
| **In-memory with disk cache** | Fast reads, no query engine | Memory usage, cold start |

**Recommendation**: Start with flat JSON for simplicity. Upgrade to SQLite + brute-force if performance is an issue. Only add sqlite-vec if needed at scale.

## Configuration Additions

```json
{
  "embedding_model": "nomic-embed-text-v2-moe",
  "embedding_chunk_size": 500,
  "embedding_top_k": 10
}
```

## Slash Commands

| Command | Description |
|---------|-------------|
| /index [dir] | Index the current or specified directory |
| /index-status | Show indexing status and stats |

## Implementation Phases

### Phase 1: Core (MVP)
- `embedder.go`: call `/api/embeddings`
- `store.go`: flat JSON vector store
- `indexer.go`: walk directory, chunk files, embed
- `search.go`: `semantic_search` tool with query embedding + cosine similarity

### Phase 2: Polish
- Incremental re-indexing (mtime-based)
- Config for embedding model and chunk size
- `/index` and `/index-status` commands
- Progress indicator during indexing

### Phase 3: Performance
- SQLite storage for larger codebases
- Background indexing
- Caching embeddings for repeated queries

## Open Questions

- **Chunking strategy**: by lines, by paragraphs, or by AST nodes (for code)?
- **File filtering**: should we index `.git/`, `vendor/`, `node_modules/`? Probably not by default.
- **Cross-file context**: should embeddings include file path/metadata in the text?
- **Model selection**: auto-detect available embedding models or require config?
