//go:build !unix

package metrics

func diskSpace(string) (free, total float64, ok bool) { return 0, 0, false }
