package links

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// RandomSlugGenerator implements SlugGenerator with crypto/rand.
type RandomSlugGenerator struct{}

var _ SlugGenerator = RandomSlugGenerator{}

// Generate returns GeneratedSlugLength characters drawn uniformly from
// GeneratedSlugAlphabet.
func (RandomSlugGenerator) Generate() (string, error) {
	alphabetSize := big.NewInt(int64(len(GeneratedSlugAlphabet)))
	slug := make([]byte, GeneratedSlugLength)
	for position := range slug {
		index, err := rand.Int(rand.Reader, alphabetSize)
		if err != nil {
			return "", fmt.Errorf("generate random slug character: %w", err)
		}
		slug[position] = GeneratedSlugAlphabet[index.Int64()]
	}
	return string(slug), nil
}
