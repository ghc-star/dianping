package repository

import "gorm.io/gorm"

// Repository holds a concurrency-safe GORM connection pool, never a request context.
type Repository struct{ DB *gorm.DB }

func New(db *gorm.DB) *Repository { return &Repository{DB: db} }
