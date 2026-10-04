package users

type User struct {
	Name, Email string
}

// New makes a user after checking the address.
func New(name, email string) (User, bool) {
	if !isValidAddress(email) {
		return User{}, false
	}
	return User{name, email}, true
}
