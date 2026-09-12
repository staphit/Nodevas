package rag

import (
	"context"
	"crypto/sha256"
	"fmt"
)

const (
	MaxSourceBytes     = 16 << 20
	maxChunks          = 10000
	embeddingBatchSize = 32
)

// Source is a body-free document manifest. Rev is the source file revision,
// separate from the title+content hash used by the existing Chroma chunks.
type Source struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Rev   string `json:"rev"`
	Bytes int    `json:"bytes"`
}

type documentIndex struct {
	Source Source
	IDs    []string
}

// Sync keeps the in-memory API for callers that already hold the documents.
func (s *Store) Sync(ctx context.Context, docs []Document) (int, error) {
	sources := make([]Source, 0, len(docs))
	byID := make(map[string]Document, len(docs))
	for _, doc := range docs {
		sources = append(sources, Source{ID: doc.ID, Title: doc.Title, Rev: Revision(doc), Bytes: len(doc.Content)})
		byID[doc.ID] = doc
	}
	return s.SyncSources(ctx, sources, func(_ context.Context, source Source) (Document, error) { return byID[source.ID], nil })
}

// SyncSources fetches changed documents one at a time and embeds in bounded
// batches. The loader must reject a source whose revision changed since listing.
// Callers serialize Sync/Search and validate retrieved chunks against live sources.
// Only a successful sync publishes the session manifest; it never retains bodies.
func (s *Store) SyncSources(ctx context.Context, sources []Source, load func(context.Context, Source) (Document, error)) (int, error) {
	totalBytes := 0
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		if source.ID == "" || seen[source.ID] {
			return 0, fmt.Errorf("document IDs must be nonempty and unique")
		}
		seen[source.ID] = true
		if source.Bytes < 0 || source.Bytes > MaxSourceBytes-totalBytes {
			return 0, fmt.Errorf("project exceeds the 16 MiB document indexing limit")
		}
		totalBytes += source.Bytes
	}
	base, err := s.collection(ctx, true)
	if err != nil {
		return 0, err
	}
	previous := map[string]bool{}
	for offset := 0; ; offset += 1000 {
		var page struct {
			IDs []string `json:"ids"`
		}
		if err := s.request(ctx, "POST", base+"/get", map[string]any{"limit": 1000, "offset": offset, "include": []string{}}, &page); err != nil {
			return 0, err
		}
		for _, id := range page.IDs {
			previous[id] = true
		}
		if len(page.IDs) < 1000 {
			break
		}
		if offset >= 100000 {
			return 0, fmt.Errorf("collection exceeds indexing limit")
		}
	}
	var pending []Hit
	var ids, inputs []string
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		vectors, err := s.embed(ctx, inputs)
		if err != nil {
			return err
		}
		texts, metadata := make([]string, len(pending)), make([]Metadata, len(pending))
		for i, chunk := range pending {
			texts[i], metadata[i] = chunk.Text, chunk.Metadata
		}
		if err := s.request(ctx, "POST", base+"/upsert", map[string]any{"ids": ids, "documents": texts, "metadatas": metadata, "embeddings": vectors}, nil); err != nil {
			return err
		}
		pending, ids, inputs = nil, nil, nil
		return nil
	}
	next := make(map[string]documentIndex, len(sources))
	count := 0
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		cached, reusable := s.synced[source.ID]
		reusable = reusable && cached.Source == source
		for _, id := range cached.IDs {
			if !previous[id] {
				reusable = false
				break
			}
		}
		if reusable {
			for _, id := range cached.IDs {
				delete(previous, id)
			}
			next[source.ID] = cached
			count += len(cached.IDs)
		} else {
			doc, err := load(ctx, source)
			if err != nil {
				return 0, err
			}
			if doc.ID != source.ID || doc.Title != source.Title || len(doc.Content) != source.Bytes {
				return 0, fmt.Errorf("documents changed during indexing; run index_documents again")
			}
			entry := documentIndex{Source: source}
			err = eachChunk(doc, func(chunk Hit) error {
				count++
				if count > maxChunks {
					return fmt.Errorf("project exceeds the 10000-chunk indexing limit")
				}
				id := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", chunk.NodeID, chunk.Revision, chunk.Start))))
				entry.IDs = append(entry.IDs, id)
				if !previous[id] {
					pending, ids = append(pending, chunk), append(ids, id)
					inputs = append(inputs, doc.Title+"\n\n"+chunk.Text)
				}
				delete(previous, id)
				if len(pending) == embeddingBatchSize {
					return flush()
				}
				return nil
			})
			if err != nil {
				return 0, err
			}
			next[source.ID] = entry
		}
		if count > maxChunks {
			return 0, fmt.Errorf("project exceeds the 10000-chunk indexing limit")
		}
	}
	if err := flush(); err != nil {
		return 0, err
	}
	// Preserve old chunks on any embedding/upsert failure, including late batches.
	var obsolete []string
	for id := range previous {
		obsolete = append(obsolete, id)
	}
	for start := 0; start < len(obsolete); start += 1000 {
		if err := s.request(ctx, "POST", base+"/delete", map[string]any{"ids": obsolete[start:min(start+1000, len(obsolete))]}, nil); err != nil {
			return 0, err
		}
	}
	s.synced = next
	return count, nil
}
