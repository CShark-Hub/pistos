package main

type User struct{ Name string }

func lookup(m map[string]*User, id string) string {
	u := m[id]
	return u.Name
}

func firstN(xs []int, n int) []int {
	out := []int{}
	for i := 0; i <= n; i++ {
		out = append(out, xs[i])
	}
	return out
}
