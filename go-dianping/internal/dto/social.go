package dto

import "github.com/learning/go-dianping/internal/model"

type ScrollResult struct {
	List    []model.Blog `json:"list"`
	MinTime int64        `json:"minTime"`
	Offset  int          `json:"offset"`
}
