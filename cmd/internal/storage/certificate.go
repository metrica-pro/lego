package storage

import "github.com/metrica-pro/lego/v5/certificate"

type Certificate struct {
	*certificate.Resource

	Origin string `json:"origin,omitempty"`
}
