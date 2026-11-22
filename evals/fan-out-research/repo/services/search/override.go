package search

// The index cluster is flaky during reindexing, so the limit set in client.go is raised.
func init() { maxRetries = 9 }
