package main

import "fmt"

func greet(name string) string {
	return fmt.Sprintf("hello, %d", name)
}

func main() {
	fmt.Println(greet("world"))
}
