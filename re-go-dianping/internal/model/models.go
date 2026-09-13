package model

import "time"

type Shop struct {
	Id         int64     `json:"id" gorm:"column:id;primaryKey"`
	Name       string    `json:"name" gorm:"column:name"`
	TypeID     int64     `gorm:"column:type_id" json:"typeId"`
	Images     string    `gorm:"column:images;size:1024" json:"images"`
	Area       string    `gorm:"column:area" json:"area"`
	Address    string    `gorm:"column:address;size:255" json:"address"`
	X          float64   `gorm:"column:x" json:"x"`
	Y          float64   `gorm:"column:y" json:"y"`
	AvgPrice   int64     `gorm:"column:avg_price" json:"avgPrice"`
	Sold       int       `gorm:"column:sold" json:"sold"`
	Comments   int       `gorm:"column:comments" json:"comments"`
	Score      int       `gorm:"column:score" json:"score"`
	OpenHours  string    `gorm:"column:open_hours" json:"openHours"`
	CreateTime time.Time `gorm:"column:create_time;autoCreateTime" json:"createTime"`
	UpdateTime time.Time `gorm:"column:update_time;autoUpdateTime" json:"updateTime"`
	Distance   *float64  `gorm:"-" json:"distance,omitempty"`
}

func (Shop) TableName() string {
	return "tb_shop"
}

type User struct {
	ID         int64     `json:"id" gorm:"column:id;primaryKey;autoIncrement"`
	Phone      string    `json:"phone,omitempty" gorm:"column:phone;size:11"`
	Password   string    `json:"-" gorm:"column:password;size:128"`
	NickName   string    `gorm:"column:nick_name;size:32" json:"nickName"`
	Icon       string    `gorm:"column:icon;size:255" json:"icon"`
	CreateTime time.Time `gorm:"column:create_time;autoCreateTime" json:"createTime"`
	UpdateTime time.Time `gorm:"column:update_time;autoUpdateTime" json:"updateTime"`
}

func (User) TableName() string {
	return "tb_user"
}

type UserInfo struct {
	UserID     int64      `gorm:"column:user_id;primaryKey;autoIncrement:false" json:"userId"`
	City       string     `gorm:"column:city" json:"city"`
	Introduce  string     `gorm:"column:introduce" json:"introduce"`
	Fans       int        `gorm:"column:fans" json:"fans"`
	Followee   int        `gorm:"column:followee" json:"followee"`
	Gender     int        `gorm:"column:gender" json:"gender"`
	Birthday   *time.Time `gorm:"column:birthday;type:date" json:"birthday,omitempty"`
	Credits    int        `gorm:"column:credits" json:"credits"`
	Level      int        `gorm:"column:level" json:"level"`
	CreateTime time.Time  `gorm:"column:create_time;autoCreateTime" json:"-"`
	UpdateTime time.Time  `gorm:"column:update_time;autoUpdateTime" json:"-"`
}

func (UserInfo) TableName() string {
	return "tb_user_info"
}

type ShopType struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name       string    `gorm:"column:name" json:"name"`
	Icon       string    `gorm:"column:icon" json:"icon"`
	Sort       int       `gorm:"column:sort" json:"sort"`
	CreateTime time.Time `gorm:"column:create_time;autoCreateTime" json:"-"`
	UpdateTime time.Time `gorm:"column:update_time;autoUpdateTime" json:"-"`
}

func (ShopType) TableName() string {
	return "tb_shop_type"
}
