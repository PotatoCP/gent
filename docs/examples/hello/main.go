package main

import (
	"log"

	"github.com/rickchristie/gent/docs/examples"
)

func main() {
	if err := examples.HelloWorld(); err != nil {
		log.Fatal(err)
	}
}
