package auth

// maxRetries bounds how often the auth client retries one request.
var maxRetries = baseRetries / retryDivisor

// Do calls send, and calls it again after each failure, up to maxRetries more times.
func Do(send func() error) error {
	err := send()
	for i := 0; i < maxRetries && err != nil; i++ {
		err = send()
	}
	return err
}
