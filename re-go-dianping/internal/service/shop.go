package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/learning/go-dianping/internal/cache"
	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/repository"
	"github.com/redis/go-redis/v9"
)

var ErrInvalidShopCreat = errors.New("商铺参数错误")

type ShopService struct {
	repo  *repository.Repository
	rdb   *redis.Client
	cache *cache.Client
}

func NewShopRepository(
	repo *repository.Repository,
	rdb *redis.Client,
) *ShopService {
	return &ShopService{
		repo:  repo,
		rdb:   rdb,
		cache: cache.New(rdb),
	}
}

func validateShop(shop *model.Shop) error {
	if strings.TrimSpace(shop.Name) == "" ||
		utf8.RuneCountInString(shop.Name) > 128 {
		return fmt.Errorf("%w：名称必填且最长128字", ErrInvalidShopCreat)
	}

	if shop.TypeID <= 0 {
		return fmt.Errorf("%w：商铺类型ID必须大于0", ErrInvalidShopCreat)
	}

	if strings.TrimSpace(shop.Address) == "" ||
		utf8.RuneCountInString(shop.Address) > 255 {
		return fmt.Errorf("%w：地址必填且最长255字", ErrInvalidShopCreat)
	}

	if utf8.RuneCountInString(shop.Images) > 1024 ||
		utf8.RuneCountInString(shop.Area) > 128 ||
		utf8.RuneCountInString(shop.OpenHours) > 32 {
		return fmt.Errorf("%w：图片、商圈或营业时间过长", ErrInvalidShopCreat)
	}

	if math.IsNaN(shop.X) || math.IsInf(shop.X, 0) ||
		math.IsNaN(shop.Y) || math.IsInf(shop.Y, 0) ||
		shop.X < -180 || shop.X > 180 ||
		shop.Y < -85.05112878 || shop.Y > 85.05112878 {
		return fmt.Errorf("%w：经纬度超出GEO支持范围", ErrInvalidShopCreat)
	}

	if shop.AvgPrice < 0 || shop.Sold < 0 ||
		shop.Comments < 0 || shop.Score < 0 || shop.Score > 50 {
		return fmt.Errorf(
			"%w：金额、销量、评论数不能为负，评分须为0到50",
			ErrInvalidShopCreat,
		)
	}

	return nil
}

func (s *ShopService) GetByID(
	ctx context.Context,
	id int64,
) (model.Shop, error) {
	var result model.Shop
	//每家商铺使用不同的缓存key
	key := "practice:shop:" + strconv.FormatInt(id, 10)
	//1.查Redis
	cached, err := s.rdb.Get(ctx, key).Result()
	if err == nil {
		//Redis保存的是JSON字符串，转回Shop
		if decodeErr := json.Unmarshal([]byte(cached), &result); decodeErr == nil {
			log.Printf("缓存命中：%s", key)
			return result, nil
		}
		log.Printf("缓存内容无法解析，重新查数据库：%s", key)
	} else if !errors.Is(err, redis.Nil) {
		// redis.Nil 表示 key 不存在，是正常的缓存未命中。
		// 其他错误表示 Redis 操作失败，记录后继续查数据库。
		log.Printf("读取缓存失败：%v", err)
	}
	//查数据库前，记住当前缓存版本
	keys := ShopKeys(id)
	version, versionErr := s.rdb.Get(ctx, keys.Version).Result()
	//版本key不存在是正常情况，用空字符串代表
	if errors.Is(versionErr, redis.Nil) {
		version = ""
		versionErr = nil
	}
	if versionErr != nil {
		log.Printf("读取商铺缓存版本失败：%v", versionErr)
	}
	// 2. 没拿到有效缓存，查 MySQL。
	log.Printf("查询 MySQL：id=%d", id)
	result, err = s.repo.FindById(ctx, id)
	if err != nil {
		return model.Shop{}, err
	}

	//3.把商铺转成JSON，写入Redis，保存5分钟
	data, err := json.Marshal(result)
	if err != nil {
		log.Printf("商铺序列化失败：%v", err)
		return result, nil
	}
	if versionErr == nil {
		stored, cacheErr := s.cache.SetIfVersion(
			ctx,
			keys,
			version,
			data,
			5*time.Minute,
		)
		if cacheErr != nil {
			log.Printf("写入商铺缓存失败：%v", cacheErr)
		} else if !stored {
			log.Printf("商铺缓存版本已变化，跳过回填：id=%d", id)
		}
	}
	//Mysql已经查成功，缓存写入失败也可以返回商铺
	return result, nil
}

func (s *ShopService) ShopCreate(ctx context.Context, shop *model.Shop) (int64, error) {
	if shop == nil {
		return 0, ErrInvalidShopCreat
	}
	if err := validateShop(shop); err != nil {
		return 0, err
	}
	exists, err := s.repo.ShopTypeExists(ctx, shop.TypeID)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, fmt.Errorf("%w：商铺类型不存在", ErrInvalidShopCreat)
	}
	shop.Id = 0
	shop.CreateTime = time.Time{}
	shop.UpdateTime = time.Time{}
	shop.Distance = nil
	if err = s.repo.ShopCreate(ctx, shop); err != nil {
		return 0, err
	}

	if err = s.SyncGeo(ctx, nil, shop); err != nil {
		s.geoFailed(ctx, err)
	}

	if err = s.cache.Invalidate(ctx, ShopKeys(shop.Id)); err != nil {
		log.Printf("商铺已创建，但缓存失效处理失败：%v", err)
	}
	return shop.Id, nil
}

func (s *ShopService) SyncGeo(
	ctx context.Context,
	old, next *model.Shop,
) error {
	pipe := s.rdb.TxPipeline()
	if old != nil && old.TypeID != next.TypeID {
		oldKey := "practice:shop:geo:" + strconv.FormatInt(old.TypeID, 10)
		pipe.ZRem(ctx, oldKey, strconv.FormatInt(old.Id, 10))
	}
	// 向当前分类写入商铺位置。
	key := "practice:shop:geo:" +
		strconv.FormatInt(next.TypeID, 10)

	pipe.GeoAdd(ctx, key, &redis.GeoLocation{
		Name:      strconv.FormatInt(next.Id, 10),
		Longitude: next.X,
		Latitude:  next.Y,
	})

	_, err := pipe.Exec(ctx)
	return err
}

const shopGeoReadyKey = "practice:shop:geo:ready"

func (s *ShopService) geoFailed(ctx context.Context, cause error) {
	log.Printf("同步商铺GEO失败:%v", cause)
	if err := s.rdb.Del(ctx, shopGeoReadyKey).Err(); err != nil {
		log.Printf("清除GEO就绪标记失败:%v", err)
	}
}

func ShopKeys(id int64) cache.Keys {
	suffix := strconv.FormatInt(id, 10)
	return cache.Keys{
		Data:    "practice:shop:" + suffix,
		Lock:    "practice:shop:lock:" + suffix,
		Version: "practice:shop:version:" + suffix,
	}
}

func (s *ShopService) ShopUpdate(ctx context.Context, in dto.ShopInput) error {
	if in.ID <= 0 || !in.HasChanges() {
		fmt.Print('1')
		return fmt.Errorf("%w：原因", ErrInvalidShopCreat)
	}
	old, err := s.repo.FindById(ctx, in.ID)
	if err != nil {
		return fmt.Errorf("%w：原因", ErrInvalidShopCreat)
	}
	next := old
	in.ApplyTo(&next)
	if err = validateShop(&next); err != nil {
		return err
	}
	if next.TypeID != old.TypeID {
		exists, err := s.repo.ShopTypeExists(ctx, next.TypeID)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w：商铺类型不存在", ErrInvalidShopCreat)
		}
	}
	if err = s.repo.ShopChange(ctx, in); err != nil {
		return err
	}
	if err = s.cache.Invalidate(ctx, ShopKeys(in.ID)); err != nil {
		log.Printf("商铺已更新，但缓存失效处理失败：%v", err)
	}
	if err = s.SyncGeo(ctx, &old, &next); err != nil {
		s.geoFailed(ctx, err)
	}
	return nil
}
