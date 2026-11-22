package billing

// extraRetries covers the payment gateway's slow settlement window.
func extraRetries() int { return 3 }
