package relayruntime

import "time"

// ProcessAlive reports whether a process with pid is still present.
func ProcessAlive(pid int) bool {
	return processAlive(pid)
}

func TerminateProcess(pid int, grace time.Duration) error {
	return terminateProcess(pid, grace)
}
