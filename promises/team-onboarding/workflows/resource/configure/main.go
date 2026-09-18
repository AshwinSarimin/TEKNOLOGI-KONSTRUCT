package main

import "log"

func main() {
	stage := getEnv("STAGE", "configure")
	switch stage {
	case "configure":
		runConfigure()
	case "watch":
		runWatcher()
	default:
		log.Fatalf("unknown STAGE=%q (expected configure or watch)", stage)
	}
}
