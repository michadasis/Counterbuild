package render

import "testing"

func BenchmarkRender(b *testing.B) {
	r := NewRenderer()
	res := sampleResult()
	for i := 0; i < b.N; i++ {
		r.Render(res, nil, Style{Scale: 1, Opacity: 0.94})
	}
}
