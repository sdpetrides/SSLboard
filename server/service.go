package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	"github.com/sdpetrides/fleet/pb"
	"github.com/boltdb/bolt"
	"golang.org/x/crypto/bcrypt"
)

/**
 * func vaildateToken
 * Authenticates a user for GET/POST/END given username/token
 */
func validateToken(token string, username string) error {

	var bucket_tokens = []byte("Tokens")
	var userNotAuth = errors.New("User is not authenticated.")
	var incorrectToken = errors.New("Incorrect session token.")

	// open database
	db, err := bolt.Open("./board.db", 0666, nil)
	if err != nil {
		return err
	}
	defer db.Close()

	// verify that username has active token listed
	err = db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucket_tokens)
		if bucket == nil {
			return userNotAuth
		}
		if token != string(bucket.Get([]byte(username))) {
			return incorrectToken
		}
		return nil
	})
	return err
}

type SSLboardServer struct{}

/**
 * func Authenticate
 * Authenticates a user given username/passwords
 */
func (s *SSLboardServer) Authenticate(ctx context.Context, c *pb.Credentials) (*pb.Credentials, error) {

	fmt.Println("\nRPC call to Authenticate()")

	var hash []byte
	var groups []string
	var bucket_users = []byte("Users")
	var bucket_tokens = []byte("Tokens")
	var bucket_groups = []byte("Groups")
	var userNotExist = errors.New("Username does not exist.")
	var userInSession = errors.New("User is currently in a session.")
	var noGroups = errors.New("No available groups.")

	// open database
	db, err := bolt.Open("./board.db", 0666, nil)
	if err != nil {
		return c, err
	}
	defer db.Close()

	// extract username from the struct passed through TLS
	username := []byte(c.Username)
	password := []byte(c.Password)

	// get username from database
	err = db.Update(func(tx *bolt.Tx) error {

		// create bucket on first Authenticate() call to server since boot
		bucket, err := tx.CreateBucketIfNotExists(bucket_users)
		if err != nil {
			return err
		}

		// get hash and salt from user bucket
		stored_hash := bucket.Get(username)
		if stored_hash == nil {
			log.Println("Username does not exist.")
			return userNotExist
		}
		hash = make([]byte, len(stored_hash))
		copy(hash, stored_hash)

		return nil
	})

	if err != nil {
		switch err {

		case userNotExist:
			// create new key pair
			hash, err := bcrypt.GenerateFromPassword(password, bcrypt.MinCost)
			if err != nil {
				panic("Error hashing password.")
			}

			// store new username and hash
			err = db.Update(func(tx *bolt.Tx) error {
				bucket, err := tx.CreateBucketIfNotExists(bucket_users)
				if err != nil {
					return err
				}
				err = bucket.Put(username, hash)
				if err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				panic("Error storing hash.")
			}
			log.Println("Added new user.")

		default:
			panic("Error opening Users bucket.")
		}

	} else {

		// check if user is currently in a session
		err = db.Update(func(tx *bolt.Tx) error {
			bucket, err := tx.CreateBucketIfNotExists(bucket_tokens)
			if err != nil {
				panic("Error opening Tokens bucket.")
			}
			token := bucket.Get(username)
			if token != nil {
				return userInSession
			}
			return nil
		})
		if err != nil {
			log.Println("User is currently in a session.")

			// TODO: DELETE CURRENT SESSION TOKEN, CREATE NEW SESSION TOKEN

			return c, err // returning userInSession error
		}

		// compare stored hashed password and password from database
		err = bcrypt.CompareHashAndPassword(hash, password)
		if err != nil {
			log.Println("Incorrect password.")
			return c, errors.New("Incorrect password.") // may want to return special error (incorrect password)
		}
	}

	log.Printf("User '%s' has been authenticated.", c.Username)

	// generate token
	token := make([]byte, 16)
	n, err := rand.Read(token)
	if n != 16 {
		panic("Error generating token.")
	}

	// store token
	err = db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(bucket_tokens)
		if err != nil {
			panic("Error opening Tokens bucket.")
		}
		err = bucket.Put(username, token)
		if err != nil {
			panic("Error writing to Tokens bucket.")
		}
		return nil
	})

	log.Println("Returning session token to user...")

	// store token in password field to send back to user
	c.Password = string(token)

	// send back list of available groups
	err = db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucket_groups)
		if bucket == nil {
			return noGroups
		}
		s := bucket.Stats()
		groups = make([]string, s.KeyN)

		c := bucket.Cursor()
		i := 0
		for k, v := c.First(); k != nil; k, v = c.Next() {
			groups[i] = string(v)
			i += 1
		}
		return nil
	})
	if err != nil {
		log.Println(err)
	}

	fmt.Println(groups)
	c.Groups = groups
	return c, nil
}

/**
 * func get
 * Handles a GET request from the client
 */
func (s *SSLboardServer) Get(_ context.Context, m *pb.Message) (*pb.Message, error) {

	fmt.Println("\nRPC call to Get()")
	log.Printf("Username: %s\n", m.Username)
	log.Printf("Group: %s\n", m.Group)

	var groups []string
	var messages []string
	var bucket_groups = []byte("Groups")
	var noGroups = errors.New("No available groups.")
	var groupNotExist = errors.New("Group does not exist.")

	token := m.Token
	username := m.Username
	group := []byte(strings.ToLower(m.Group))

	// check that the token given is a valid token
	err := validateToken(token, username)
	if err != nil {
		log.Println(err)
		return m, err
	}

	// open database
	db, err := bolt.Open("./board.db", 0666, nil)
	if err != nil {
		log.Println("Error opening database.")
		return m, err
	}
	defer db.Close()

	// send back list of available groups
	err = db.View(func(tx *bolt.Tx) error {
		bucket1 := tx.Bucket(bucket_groups)
		if bucket1 == nil {
			return noGroups
		}
		s1 := bucket1.Stats()
		groups = make([]string, s1.KeyN)

		c1 := bucket1.Cursor()
		i := 0
		for k, v := c1.First(); k != nil; k, v = c1.Next() {
			groups[i] = string(v)
			i += 1
		}

		bucket2 := tx.Bucket(group)
		if bucket2 == nil {
			return groupNotExist
		}
		s2 := bucket2.Stats()
		messages = make([]string, s2.KeyN)

		c2 := bucket2.Cursor()
		j := 0
		for k, v := c2.First(); k != nil; k, v = c2.Next() {
			var buffer bytes.Buffer
			buffer.WriteString(string(k))
			buffer.WriteString("\t")
			buffer.WriteString(string(v))
			messages[j] = buffer.String()
			j += 1
		}
		return nil
	})
	if err != nil {
		log.Println(err)
	}

	fmt.Println(groups)
	fmt.Println(messages)
	m.Groups = groups
	m.Messages = messages
	return m, nil
}

/**
 * func post
 * Handles a POST request from the client
 */
func (s *SSLboardServer) Post(_ context.Context, m *pb.Message) (*pb.Message, error) {

	fmt.Println("\nRPC call to Post()")
	log.Printf("Username: %s\n", m.Username)
	log.Printf("Group: %s\n", m.Group)
	log.Printf("Message: %s\n", m.Msg)

	var groups []string
	var bucket_groups = []byte("Groups")
	var noGroups = errors.New("No available groups.")

	token := m.Token
	username := m.Username
	group := []byte(strings.ToLower(m.Group))
	message := []byte(m.Msg)

	// check that the token given is a valid token
	err := validateToken(token, username)
	if err != nil {
		log.Println(err)
		return m, err
	}

	// open database
	db, err := bolt.Open("./board.db", 0666, nil)
	if err != nil {
		log.Println("Error opening database.")
		return m, err
	}
	defer db.Close()

	// POST message to a given group, add timestamp, and return success message
	err = db.Update(func(tx *bolt.Tx) error {
		bucket1, err := tx.CreateBucketIfNotExists(bucket_groups)
		if err != nil {
			panic("Error opening bucket.")
		}
		err = bucket1.Put(group, group)
		if err != nil {
			panic("Error writing to Tokens bucket.")
		}
		bucket2, err := tx.CreateBucketIfNotExists(group)
		if err != nil {
			panic("Error opening bucket.")
		}

		// cat USERNAME:message
		var buffer bytes.Buffer
		buffer.WriteString(" ")
		buffer.WriteString(m.Username)
		buffer.WriteString("\t")
		buffer.WriteString(string(message))

		err = bucket2.Put([]byte(string(time.Now().Format(time.RFC850))), []byte(buffer.String()))
		if err != nil {
			panic("Error writing to Tokens bucket.")
		}
		return nil
	})

	fmt.Println("Posted message to bucket.")

	// send back list of available groups
	err = db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucket_groups)
		if bucket == nil {
			return noGroups
		}
		s := bucket.Stats()
		groups = make([]string, s.KeyN)

		c := bucket.Cursor()
		i := 0
		for k, v := c.First(); k != nil; k, v = c.Next() {
			groups[i] = string(v)
			i += 1
		}
		return nil
	})
	if err != nil {
		log.Println(err)
	}

	fmt.Println(groups)
	m.Groups = groups

	return m, nil
}

/**
 * func end
 * Handles an END request from the client
 */
func (s *SSLboardServer) End(_ context.Context, m *pb.Message) (*pb.Message, error) {

	fmt.Println("\nRPC call to End()")

	var bucket_tokens = []byte("Tokens")

	token := m.Token
	username := m.Username

	err := validateToken(token, username)
	if err != nil {
		log.Println(err)
		return m, err
	}

	// open database
	db, err := bolt.Open("./board.db", 0666, nil)
	if err != nil {
		return m, err
	}
	defer db.Close()

	// remove client token from list of active tokens
	err = db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(bucket_tokens)
		if err != nil {
			panic("Error opening Tokens bucket.")
		}
		err = bucket.Delete([]byte(username))
		if err != nil {
			panic("Error writing to Tokens bucket.")
		}
		return nil
	})

	log.Printf("Active session for user '%s' terminated.", m.Username)

	return m, nil
}
