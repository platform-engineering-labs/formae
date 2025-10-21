// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

type Config struct {
	*User

	AuthorizedUsers []User
}

type User struct {
	Username string
	Password string
}

func (c Config) GetUser(username string) *User {
	for _, u := range c.AuthorizedUsers {
		if u.Username == username {
			return &u
		}
	}

	return nil
}
