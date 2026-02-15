package domain

type SortOrder string

const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)

type TorrentFilter struct {
	Status    *TorrentStatus `json:"status,omitempty"`
	Search    string         `json:"search,omitempty"`
	Tags      []string       `json:"tags,omitempty"`
	SortBy    string         `json:"sortBy,omitempty"`
	SortOrder SortOrder      `json:"sortOrder,omitempty"`
	Limit     int            `json:"limit,omitempty"`
	Offset    int            `json:"offset,omitempty"`
}
