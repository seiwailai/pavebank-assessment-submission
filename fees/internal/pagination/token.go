package pagination

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Token interface {
	Validate() error
}

type LineItemPaginationToken struct {
	OccurredAt time.Time `json:"occurred_at"`
	LineItemID string    `json:"line_item_id"`
}

func (t *LineItemPaginationToken) Validate() error {
	if t == nil {
		return fmt.Errorf("pagination token is required")
	}
	if t.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at is required")
	}
	if t.LineItemID == "" {
		return fmt.Errorf("line_item_id is required")
	}
	parsed, err := uuid.Parse(t.LineItemID)
	if err != nil {
		return fmt.Errorf("line_item_id must be a valid uuidv4")
	}
	if parsed.Version() != 4 {
		return fmt.Errorf("line_item_id must be a valid uuidv4")
	}
	return nil
}

func Encode(token Token) (string, error) {
	if token == nil {
		return "", fmt.Errorf("pagination token is required")
	}
	if err := token.Validate(); err != nil {
		return "", err
	}

	body, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("marshal pagination token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func Decode(encoded string, token Token) error {
	if encoded == "" {
		return fmt.Errorf("page_token is required")
	}
	if token == nil {
		return fmt.Errorf("pagination token target is required")
	}

	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode pagination token: %w", err)
	}
	if err := json.Unmarshal(body, token); err != nil {
		return fmt.Errorf("unmarshal pagination token: %w", err)
	}
	if err := token.Validate(); err != nil {
		return err
	}
	return nil
}
