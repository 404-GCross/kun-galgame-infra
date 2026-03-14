package utils

// Pagination holds pagination parameters
type Pagination struct {
	Page     int `json:"page" query:"page"`
	PageSize int `json:"page_size" query:"page_size"`
}

// PaginatedResult holds paginated query results
type PaginatedResult[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// DefaultPagination returns default pagination values
func DefaultPagination() Pagination {
	return Pagination{
		Page:     1,
		PageSize: 20,
	}
}

// Normalize ensures pagination values are within valid ranges
func (p *Pagination) Normalize() {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 20
	}
	if p.PageSize > 100 {
		p.PageSize = 100
	}
}

// Offset returns the offset for database queries
func (p *Pagination) Offset() int {
	return (p.Page - 1) * p.PageSize
}

// Limit returns the limit for database queries
func (p *Pagination) Limit() int {
	return p.PageSize
}

// NewPaginatedResult creates a new PaginatedResult
func NewPaginatedResult[T any](items []T, total int64, page, pageSize int) PaginatedResult[T] {
	return PaginatedResult[T]{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}
}
