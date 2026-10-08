package infra

import "example.com/shop/internal/domain"

var _ = domain.Svc{}

func Save(n int) int { return n }
