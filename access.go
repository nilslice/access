package access

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/boltdb/bolt"
	"github.com/nilslice/jwt"

	"github.com/ponzu-cms/ponzu/system/admin/user"
	"github.com/ponzu-cms/ponzu/system/db"
)

const (
	apiAccessStore      = "__apiAccess"
	apiPendingUserStore = "__apiPending"
	apiAccessCookie     = "_apiAccessToken"
)

// APIAccess is the data for an API access grant
type APIAccess struct {
	Key   string `json:"key"`
	Hash  string `json:"hash"`
	Salt  string `json:"salt"`
	Token string `json:"token"`
}

// Config contains settings for token creation and validation
type Config struct {
	ExpireAfter    time.Duration
	ResponseWriter http.ResponseWriter
	TokenStore     reqHeaderOrHTTPCookie
	CustomClaims   map[string]interface{}
	SecureCookie   bool
}

type reqHeaderOrHTTPCookie interface{}

func init() {
	db.AddBucket(apiAccessStore)
	db.AddBucket(apiPendingUserStore)
}

// Grant creates a new APIAccess and saves it to the __apiAccess bucket in the database
// and if an existing APIAccess grant is encountered in the database, Grant attempts
// to update the grant but will fail if unauthorized
func Grant(key, password string, cfg *Config) (*APIAccess, error) {
	if key == "" {
		return nil, fmt.Errorf("%s", "key must not be empty")
	}

	if password == "" {
		return nil, fmt.Errorf("%s", "password must not be empty")
	}

	u, err := user.New(key, password)
	if err != nil {
		return nil, err
	}

	apiAccess := &APIAccess{
		Key:  u.Email,
		Hash: u.Hash,
		Salt: u.Salt,
	}

	err = apiAccess.setToken(cfg)
	if err != nil {
		return nil, err
	}

	err = db.Store().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(apiAccessStore))
		if b == nil {
			return fmt.Errorf("failed to get bucket %s", apiAccessStore)
		}

		if b.Get([]byte(apiAccess.Key)) != nil {
			err := updateGrant(key, password, cfg)
			if err != nil {
				return fmt.Errorf("failed to update APIAccess grant for %s, %v", apiAccess.Key, err)
			}
		}

		j, err := json.Marshal(u)
		if err != nil {
			return fmt.Errorf("failed to marshal APIAccess to json, %v", err)
		}

		return b.Put([]byte(apiAccess.Key), j)
	})

	err = db.Store().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(apiPendingUserStore))
		if b == nil {
			return fmt.Errorf("failed to get bucket %s", apiPendingUserStore)
		}

		if b.Get([]byte(apiAccess.Key)) != nil {
			b.Delete([]byte(apiAccess.Key))
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return apiAccess, nil
}

// Login attempts
// to update the grant but will fail if unauthorized
func Login(key, password string, cfg *Config) (*APIAccess, error) {
	if key == "" {
		return nil, fmt.Errorf("%s", "key must not be empty")
	}

	if password == "" {
		return nil, fmt.Errorf("%s", "password must not be empty")
	}

	u, err := user.New(key, password)
	if err != nil {
		return nil, err
	}

	apiAccess := &APIAccess{
		Key:  u.Email,
		Hash: u.Hash,
		Salt: u.Salt,
	}

	err = apiAccess.setToken(cfg)
	if err != nil {
		return nil, err
	}

	err = db.Store().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(apiAccessStore))
		if b == nil {
			return fmt.Errorf("failed to get bucket %s", apiAccessStore)
		}

		if b.Get([]byte(apiAccess.Key)) != nil {
			err := updateGrant(key, password, cfg)
			if err != nil {
				return fmt.Errorf("failed to update APIAccess grant for %s, %v", apiAccess.Key, err)
			}
			return nil
		}

		return fmt.Errorf("%s", "User Not Authorized")
	})

	if err != nil {
		return nil, err
	}

	return apiAccess, nil
}

// Check is to see if the user exists in either active or pending status
func Check(key string) error {
	if key == "" {
		return fmt.Errorf("%s", "key must not be empty")
	}

	err := db.Store().View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(apiAccessStore))
		if b == nil {
			return fmt.Errorf("failed to get bucket %s", apiAccessStore)
		}

		if b.Get([]byte(key)) != nil {
			return fmt.Errorf("%s", "email already actively in use")
		}

		return nil
	})

	err = db.Store().View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(apiPendingUserStore))
		if b == nil {
			return fmt.Errorf("failed to get bucket %s", apiPendingUserStore)
		}

		if b.Get([]byte(key)) != nil {
			return fmt.Errorf("%s", "email already pending in use")
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// Pending adds user to pending status to block possible duplicates
func Pending(key string) error {
	if key == "" {
		return fmt.Errorf("Pending: %s", "key must not be empty")
	}

	err := db.Store().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(apiPendingUserStore))
		if b == nil {
			return fmt.Errorf("Pending: failed to get bucket %s", apiPendingUserStore)
		}

		if b.Get([]byte(key)) != nil {
			return fmt.Errorf("Pending: %s", "email already in use")
		}

		return b.Put([]byte(key), []byte("pending"))
	})

	if err != nil {
		return err
	}

	return nil
}

// ClearPending removes the user from pending status db
func ClearPending(key string) error {
	if key == "" {
		return fmt.Errorf("Pending: %s", "key must not be empty")
	}

	err := db.Store().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(apiPendingUserStore))
		if b == nil {
			return fmt.Errorf("Pending: failed to get bucket %s", apiPendingUserStore)
		}

		if b.Get([]byte(key)) != nil {
			b.Delete([]byte(key))
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// ClearGrant removes the user from active status db
func ClearGrant(key string) error {
	if key == "" {
		return fmt.Errorf("Grant: %s", "key must not be empty")
	}

	err := db.Store().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(apiAccessStore))
		if b == nil {
			return fmt.Errorf("Grant: failed to get bucket %s", apiAccessStore)
		}

		if b.Get([]byte(key)) != nil {
			b.Delete([]byte(key))
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// IsGranted checks if the user request is authenticated by the token held within
// the provided tokenStore (should be a http.Cookie or http.Header)
func IsGranted(req *http.Request, tokenStore reqHeaderOrHTTPCookie) bool {
	token, err := getToken(req, tokenStore)
	if err != nil {
		log.Println("failed to get token to check API access grant")
		return false
	}

	return jwt.Passes(token)
}

// IsOwner validates the access token and checks the claims within the
// authenticated request's JWT for the key key associated with the grant.
func IsOwner(req *http.Request, tokenStore reqHeaderOrHTTPCookie, key string) bool {
	token, err := getToken(req, tokenStore)
	if err != nil {
		log.Println("failed to get token to check API access owner")
		return false
	}

	if !jwt.Passes(token) {
		return false
	}

	claims := jwt.GetClaims(token)
	if claims["access"].(string) != key {
		return false
	}

	return true
}

func updateGrant(key, password string, cfg *Config) error {
	apiAccess := new(APIAccess)
	err := db.Store().View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(apiAccessStore))
		if b == nil {
			return fmt.Errorf("failed to get %s bucket to update grant", apiAccessStore)
		}

		j := b.Get([]byte(key))
		fmt.Println("Raw DB Response:\n" + string(j) + "\nEnd Raw Response\n")
		return json.Unmarshal(j, &apiAccess)
	})
	if err != nil {
		return fmt.Errorf("failed to get access grant to update grant, %v", err)
	}

	usr := &user.User{
		Email: apiAccess.Key,
		Hash:  apiAccess.Hash,
		Salt:  apiAccess.Salt,
	}

	if !user.IsUser(usr, password) {
		return fmt.Errorf(
			"unauthorized attempt to update grant for %s", apiAccess.Key,
		)
	}

	return nil
}

func getToken(req *http.Request, tokenStore reqHeaderOrHTTPCookie) (string, error) {
	switch tokenStore.(type) {
	case http.Cookie:
		cookie, err := req.Cookie(apiAccessCookie)
		if err != nil {
			return "", err
		}

		return cookie.Value, nil

	case http.Header:
		bearer := req.Header.Get("Authorization")
		return strings.TrimPrefix(bearer, "Bearer "), nil

	default:
		return "", fmt.Errorf("%s", "unrecognized token store")
	}
}

func (a *APIAccess) setToken(cfg *Config) error {
	exp := time.Now().Add(cfg.ExpireAfter)
	claims := map[string]interface{}{
		"exp":    exp.Unix(),
		"access": a.Key,
	}

	for k, v := range cfg.CustomClaims {
		if _, ok := claims[k]; ok {
			return fmt.Errorf(
				"custom Config claim [%s] collides with internal claim [%s], %s",
				k, k, "please rename custom claim",
			)
		}

		claims[k] = v
	}

	token, err := jwt.New(claims)
	if err != nil {
		return err
	}

	a.Token = token

	switch cfg.TokenStore.(type) {
	case http.Header:
		cfg.ResponseWriter.Header().Add("Authorization", "Bearer "+token)

	case http.Cookie:
		http.SetCookie(cfg.ResponseWriter, &http.Cookie{
			Name:     apiAccessCookie,
			Value:    token,
			Expires:  exp,
			Path:     "/",
			HttpOnly: true,
			Secure:   cfg.SecureCookie,
		})

	default:
		return fmt.Errorf("%s", "unrecognized token store")
	}

	return nil
}

// GateKeeper is the auth HandlerFunc, because we cannot use item.Hideable for our data without blocking references from other items
func GateKeeper(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		if IsGranted(req, req.Header) || user.IsValid(req) || trimPortFromAddress(req.RemoteAddr) == db.ConfigCache("bind_addr").(string) {
			next.ServeHTTP(res, req)
		} else {
			res.WriteHeader(http.StatusUnauthorized)
			res.Write([]byte("Please login first..."))
			fmt.Println("Request:")
			s := reflect.ValueOf(req).Elem()
			for i := 0; i < s.NumField(); i++ {
				fmt.Printf("%s: %s\n", s.Type().Field(i).Name, fmt.Sprint(s.Field(i).Interface()))
			}
		}
	})
}

func trimPortFromAddress(s string) string {
	if idx := strings.Index(s, ":"); idx != -1 {
		return s[:idx]
	}
	return s
}
