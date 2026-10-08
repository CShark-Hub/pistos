package domain

import (
	"fmt"

	"example.com/shop/internal/infra"
)

type Svc struct{}

func (s *Svc) Do(a, b int, c ...string) int { return infra.Save(a) + b + len(c) }

func complex(a, b int, c ...string) int {
	total := 0
	for i := 0; i < a && i < b; i++ {
		if i%2 == 0 {
			total += i
		} else if i%3 == 0 || i%5 == 0 {
			total -= i
		} else {
			switch {
			case i > 10:
				total++
			default:
				total--
			}
		}
	}
	fmt.Println(c)
	return total
}
