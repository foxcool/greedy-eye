package main

import (
	"fmt"
)

const ServiceName = "EYE"

var (
	version = "No Version Provided"
)

func main() {
	config := getConfig()
	fmt.Printf("Started: %v\n", config)
}

