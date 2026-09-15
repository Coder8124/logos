package deadend

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"math"

	"github.com/Coder8124/logos/internal/provider"
)

// A cache, not a record: every row is the embedding of ruling text that lives
// in the vault's markdown, so deleting the index costs one slower check and
// nothing else. Keyed by the text rather than by checkpoint, because the same
// ruling reaches the corpus from a checkpoint and from the note it was folded
// out of, and an edit to the text is a different ruling that needs a new vector.
const vectorSchema = `CREATE TABLE IF NOT EXISTS ruling_vectors (
    fingerprint TEXT NOT NULL,
    model       TEXT NOT NULL,
    vec         BLOB NOT NULL,
    PRIMARY KEY (fingerprint, model)
)`

// embedBatch caps one embeddings request. A single request holding the whole
// corpus failed intermittently at 900 inputs with a reset connection.
const embedBatch = 256

// rulingVectors returns one vector per text, embedding only the texts the cache
// has no vector for under this model. A text whose batch failed gets a nil
// vector, which scores 0, and the first such error is returned so the caller
// can say the semantic check did not cover everything.
func rulingVectors(db *sql.DB, p *provider.Provider, model string, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	cached := map[string][]float32{}
	useCache := db != nil
	if useCache {
		if _, err := db.Exec(vectorSchema); err != nil {
			useCache = false
		}
	}
	if useCache {
		if rows, err := db.Query(`SELECT fingerprint, vec FROM ruling_vectors WHERE model = ?`, model); err == nil {
			for rows.Next() {
				var fp string
				var blob []byte
				if rows.Scan(&fp, &blob) == nil {
					cached[fp] = blobToFloats(blob)
				}
			}
			rows.Close()
		}
	}

	current := map[string]bool{}
	pending := map[string][]int{} // fingerprint → every position holding that text
	var order []string
	for i, text := range texts {
		fp := fingerprint(text)
		current[fp] = true
		if v, ok := cached[fp]; ok {
			vecs[i] = v
			continue
		}
		if _, seen := pending[fp]; !seen {
			order = append(order, fp)
		}
		pending[fp] = append(pending[fp], i)
	}

	var firstErr error
	for start := 0; start < len(order); start += embedBatch {
		chunk := order[start:min(start+embedBatch, len(order))]
		inputs := make([]string, len(chunk))
		for j, fp := range chunk {
			inputs[j] = texts[pending[fp][0]]
		}
		got, err := p.Embed(model, inputs)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for j, fp := range chunk {
			for _, i := range pending[fp] {
				vecs[i] = got[j]
			}
		}
		if useCache {
			storeVectors(db, model, chunk, got)
		}
	}

	// Rulings are rarely removed, but a vector for text no longer in the corpus
	// is dead weight loaded on every call. Only pruned after a complete pass, so
	// a failed batch never discards vectors that are merely unreachable today.
	if useCache && firstErr == nil {
		for fp := range cached {
			if !current[fp] {
				db.Exec(`DELETE FROM ruling_vectors WHERE fingerprint = ? AND model = ?`, fp, model)
			}
		}
	}
	return vecs, firstErr
}

// storeVectors writes one batch in a transaction. A failed write is not an
// error for the check: the vectors were used, and the next call embeds again.
func storeVectors(db *sql.DB, model string, fps []string, vecs [][]float32) {
	tx, err := db.Begin()
	if err != nil {
		return
	}
	for j, fp := range fps {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO ruling_vectors (fingerprint, model, vec) VALUES (?, ?, ?)`,
			fp, model, floatsToBlob(vecs[j])); err != nil {
			tx.Rollback()
			return
		}
	}
	tx.Commit()
}

func fingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func floatsToBlob(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func blobToFloats(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}
