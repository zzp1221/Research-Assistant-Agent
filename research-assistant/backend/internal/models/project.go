package models

import "time"

// Project groups plans (and later papers/datasets/reports) under one research
// topic. It is owner-scoped metadata; plans reference it by ID.
type Project struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"owner_id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Archived    bool      `json:"archived,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
