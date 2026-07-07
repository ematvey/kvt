package search

import (
	"sort"
)

type Hit struct {
	DocPath      string
	Title        string
	Type         string
	Snippet      string
	Text         string
	ChunkOrdinal int
	Score        float64
}

type RankedList struct {
	Weight float64
	Hits   []Hit
}

func FuseRRF(lists []RankedList, k float64) []Hit {
	if k <= 0 {
		k = 60
	}
	combined := map[string]Hit{}
	scores := map[string]float64{}

	for _, list := range lists {
		weight := list.Weight
		if weight == 0 {
			weight = 1
		}
		for i, hit := range dedupeByDoc(list.Hits) {
			if hit.DocPath == "" {
				continue
			}
			if _, ok := combined[hit.DocPath]; !ok {
				combined[hit.DocPath] = hit
			}
			scores[hit.DocPath] += weight / (k + float64(i+1))
		}
	}

	out := make([]Hit, 0, len(combined))
	for docPath, hit := range combined {
		hit.Score = scores[docPath]
		out = append(out, hit)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].DocPath < out[j].DocPath
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func dedupeByDoc(hits []Hit) []Hit {
	out := make([]Hit, 0, len(hits))
	seen := map[string]struct{}{}
	for _, hit := range hits {
		if hit.DocPath == "" {
			continue
		}
		if _, ok := seen[hit.DocPath]; ok {
			continue
		}
		seen[hit.DocPath] = struct{}{}
		out = append(out, hit)
	}
	return out
}
