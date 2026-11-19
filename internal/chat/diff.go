package chat

import "strings"

type DiffLine struct {
	Op   byte
	Text string
}

const maxDiffCells = 4_000_000

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

func LineDiff(a, b string) []DiffLine {
	x, y := splitLines(a), splitLines(b)
	n, m := len(x), len(y)
	if n*m > maxDiffCells {
		out := make([]DiffLine, 0, n+m)
		for _, l := range x {
			out = append(out, DiffLine{'-', l})
		}
		for _, l := range y {
			out = append(out, DiffLine{'+', l})
		}
		return out
	}
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []DiffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case x[i] == y[j]:
			out = append(out, DiffLine{' ', x[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{'-', x[i]})
			i++
		default:
			out = append(out, DiffLine{'+', y[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{'-', x[i]})
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{'+', y[j]})
	}
	return out
}

func DiffStats(d []DiffLine) (added, removed int) {
	for _, l := range d {
		switch l.Op {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return
}
