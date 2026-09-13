package dto

type User struct {
	ID       int64  `json:"id"`
	NickName string `json:"nickName"`
	Icon     string `json:"icon"`
}
type Login struct {
	Phone    string `json:"phone" binding:"required,len=11"`
	Code     string `json:"code" binding:"omitempty,len=6,numeric"`
	Password string `json:"password" binding:"omitempty,min=8,max=72"`
}
