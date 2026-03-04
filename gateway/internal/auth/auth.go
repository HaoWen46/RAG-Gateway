package auth

import (
	"crypto/rsa"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Claims holds the fields we extract from a validated JWT.
type Claims struct {
	UserID string
	Role   string
}

// JWTMiddleware returns a Gin handler that validates JWT tokens.
//
// Supported algorithms:
//   - HS256 — validated with jwtSecret
//   - RS256 — validated with rsaPublicKey (nil disables RS256)
//
// On success, sets "user_id" and "role" in the Gin context.
// On failure, returns 401 with a generic message (no token internals leaked).
func JWTMiddleware(jwtSecret string, rsaPublicKey *rsa.PublicKey) gin.HandlerFunc {
	secretBytes := []byte(jwtSecret)

	keyFunc := func(t *jwt.Token) (any, error) {
		switch t.Method.(type) {
		case *jwt.SigningMethodHMAC:
			return secretBytes, nil
		case *jwt.SigningMethodRSA:
			if rsaPublicKey == nil {
				return nil, jwt.ErrTokenSignatureInvalid
			}
			return rsaPublicKey, nil
		default:
			return nil, jwt.ErrTokenSignatureInvalid
		}
	}

	return func(c *gin.Context) {
		token, err := extractBearer(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		parsed, err := jwt.Parse(token, keyFunc,
			jwt.WithValidMethods([]string{"HS256", "RS256"}),
			jwt.WithExpirationRequired(),
		)
		if err != nil || !parsed.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		mapClaims, ok := parsed.Claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		userID, _ := mapClaims["sub"].(string)
		role, _ := mapClaims["role"].(string)
		if userID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Set("user_id", userID)
		c.Set("role", role)
		c.Next()
	}
}

// extractBearer pulls the raw token string from "Authorization: Bearer <token>".
func extractBearer(c *gin.Context) (string, error) {
	header := c.GetHeader("Authorization")
	if header == "" || !strings.HasPrefix(header, "Bearer ") {
		return "", jwt.ErrTokenMalformed
	}
	token := strings.TrimPrefix(header, "Bearer ")
	if token == "" {
		return "", jwt.ErrTokenMalformed
	}
	return token, nil
}

// LoadRSAPublicKey reads a PEM-encoded RSA public key from a file.
// Returns nil (no error) if path is empty, so RS256 is simply disabled.
func LoadRSAPublicKey(path string) (*rsa.PublicKey, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return jwt.ParseRSAPublicKeyFromPEM(data)
}
