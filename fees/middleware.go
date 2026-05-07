package fees

import (
	enmiddleware "encore.dev/middleware"

	idemmiddleware "encore.app/fees/internal/middleware/idempotency"
)

//encore:middleware target=tag:idempotent
func (s *Service) Idempotency(req enmiddleware.Request, next enmiddleware.Next) enmiddleware.Response {
	return idemmiddleware.Handle(req, next, idemmiddleware.Dependencies{
		Store: s.idempotencyStore,
		Now:   s.now,
	})
}
