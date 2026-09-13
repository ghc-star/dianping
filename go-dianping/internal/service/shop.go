package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/learning/go-dianping/internal/cache"
	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/redisx"
	"github.com/learning/go-dianping/pkg/apperror"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

type ShopService struct {
	repo      shopStore
	rdb       *redis.Client
	log       *zap.Logger
	cache     *cache.Client
	geoMu     sync.Mutex
	geoFlight singleflight.Group
}

func NewShop(repo shopStore, rdb *redis.Client, log *zap.Logger, root context.Context) *ShopService {
	if log == nil {
		log = zap.NewNop()
	}
	return &ShopService{repo: repo, rdb: rdb, log: log, cache: cache.New(rdb, log, root, cache.DefaultOptions())}
}

func shopKeys(id int64) cache.Keys {
	return cache.Keys{Data: redisx.Key(redisx.ShopCachePrefix, id), Lock: redisx.Key(redisx.ShopLockPrefix, id), Version: redisx.Key(redisx.ShopVersionPrefix, id)}
}

func (s *ShopService) GetByID(ctx context.Context, id int64) (*model.Shop, error) {
	if id <= 0 {
		return nil, apperror.BadRequest("商铺 id 必须大于 0")
	}
	data, err := s.cache.Get(ctx, shopKeys(id), func(ctx context.Context) ([]byte, error) {
		shop, err := s.repo.ShopGet(ctx, id)
		if err != nil {
			return nil, err
		}
		return json.Marshal(shop)
	})
	if err != nil {
		return nil, err
	}
	var shop model.Shop
	if err = json.Unmarshal(data, &shop); err != nil {
		return nil, fmt.Errorf("decode shop cache: %w", err)
	}
	return &shop, nil
}

func validCoordinates(x, y float64) bool {
	return !math.IsNaN(x) && !math.IsNaN(y) && !math.IsInf(x, 0) && !math.IsInf(y, 0) && x >= -180 && x <= 180 && y >= -85.05112878 && y <= 85.05112878
}

func validateShop(shop *model.Shop) error {
	if strings.TrimSpace(shop.Name) == "" || utf8.RuneCountInString(shop.Name) > 128 {
		return apperror.BadRequest("商铺名称必填且最长 128 字")
	}
	if shop.TypeID <= 0 {
		return apperror.BadRequest("商铺类型 id 必须大于 0")
	}
	if strings.TrimSpace(shop.Address) == "" || utf8.RuneCountInString(shop.Address) > 255 {
		return apperror.BadRequest("地址必填且最长 255 字")
	}
	if utf8.RuneCountInString(shop.Images) > 1024 || utf8.RuneCountInString(shop.Area) > 128 || utf8.RuneCountInString(shop.OpenHours) > 32 {
		return apperror.BadRequest("图片、商圈或营业时间超过字段长度")
	}
	if !validCoordinates(shop.X, shop.Y) {
		return apperror.BadRequest("经纬度不在 Redis GEO 支持范围内")
	}
	if shop.AvgPrice < 0 || shop.Sold < 0 || shop.Comments < 0 || shop.Score < 0 || shop.Score > 50 {
		return apperror.BadRequest("金额、销量和评论数不能为负，评分须为 0 到 50")
	}
	return nil
}

func (s *ShopService) Create(ctx context.Context, shop *model.Shop) (int64, error) {
	if shop == nil {
		return 0, apperror.BadRequest("商铺不能为空")
	}
	if err := validateShop(shop); err != nil {
		return 0, err
	}
	exists, err := s.repo.ShopTypeExists(ctx, shop.TypeID)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, apperror.BadRequest("商铺类型不存在")
	}
	shop.ID = 0
	shop.CreateTime = time.Time{}
	shop.UpdateTime = time.Time{}
	shop.Distance = nil
	s.geoMu.Lock()
	defer s.geoMu.Unlock()
	if err = s.repo.ShopCreate(ctx, shop); err != nil {
		return 0, err
	}
	if err = s.syncGeo(ctx, nil, shop); err != nil {
		// MySQL is committed. Clear the readiness marker so the next nearby query repairs GEO.
		s.geoFailed(ctx, err)
	}
	if err = s.cache.Invalidate(ctx, shopKeys(shop.ID)); err != nil {
		s.log.Warn("new shop cache invalidation failed", zap.Error(err))
	}
	return shop.ID, nil
}

// Update accepts a typed patch; persistence owns database column names.
func (s *ShopService) Update(ctx context.Context, id int64, updates dto.ShopInput) error {
	if id <= 0 {
		return apperror.BadRequest("商铺 id 必须大于 0")
	}
	if !updates.HasChanges() {
		return apperror.BadRequest("没有可更新的商铺字段")
	}
	s.geoMu.Lock()
	defer s.geoMu.Unlock()
	old, err := s.repo.ShopGet(ctx, id)
	if err != nil {
		return err
	}
	next := *old
	updates.ApplyTo(&next)
	if err = validateShop(&next); err != nil {
		return err
	}
	if next.TypeID != old.TypeID {
		exists, e := s.repo.ShopTypeExists(ctx, next.TypeID)
		if e != nil {
			return e
		}
		if !exists {
			return apperror.BadRequest("商铺类型不存在")
		}
	}
	if err = s.repo.ShopUpdate(ctx, id, updates); err != nil {
		return err
	}
	// Invalidation happens after the SQL statement commits, not inside an uncommitted transaction.
	cacheErr := s.cache.Invalidate(ctx, shopKeys(id))
	if err = s.syncGeo(ctx, old, &next); err != nil {
		s.geoFailed(ctx, err)
	}
	if cacheErr != nil {
		return apperror.Unavailable("商铺已更新，但缓存删除失败，请重试本次更新")
	}
	return nil
}

func validatePage(current int) error {
	if current < 1 || current > 1000 {
		return apperror.BadRequest("current 必须为 1 到 1000")
	}
	return nil
}

func (s *ShopService) ListByName(ctx context.Context, name string, current int) ([]model.Shop, error) {
	if err := validatePage(current); err != nil {
		return nil, err
	}
	return s.repo.ShopListByName(ctx, strings.TrimSpace(name), current, 10)
}

func (s *ShopService) ListByType(ctx context.Context, typeID int64, current int, x, y *float64) ([]model.Shop, error) {
	if typeID <= 0 {
		return nil, apperror.BadRequest("typeId 必须大于 0")
	}
	if err := validatePage(current); err != nil {
		return nil, err
	}
	if x == nil && y == nil {
		return s.repo.ShopListByType(ctx, typeID, current, 5)
	}
	if x == nil || y == nil || !validCoordinates(*x, *y) {
		return nil, apperror.BadRequest("x 和 y 必须同时提供有效经纬度")
	}
	ready, err := s.rdb.Exists(ctx, redisx.ShopGeoReadyKey).Result()
	if err != nil {
		return nil, apperror.Unavailable("附近商铺索引暂时不可用")
	}
	if ready == 0 {
		if _, err, _ = s.geoFlight.Do(redisx.ShopGeoReadyKey, func() (any, error) { return nil, s.WarmGeo(ctx) }); err != nil {
			return nil, err
		}
	}
	from, end := (current-1)*5, current*5
	items, err := s.rdb.GeoSearchLocation(ctx, redisx.Key(redisx.ShopGeoPrefix, typeID), &redis.GeoSearchLocationQuery{
		GeoSearchQuery: redis.GeoSearchQuery{Longitude: *x, Latitude: *y, Radius: 5000, RadiusUnit: "m", Sort: "ASC", Count: end}, WithDist: true,
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(items) <= from {
		return []model.Shop{}, nil
	}
	items = items[from:]
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		id, e := strconv.ParseInt(item.Name, 10, 64)
		if e != nil {
			return nil, fmt.Errorf("invalid GEO member: %w", e)
		}
		ids = append(ids, id)
	}
	shops, err := s.repo.ShopListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]model.Shop, len(shops))
	for _, shop := range shops {
		byID[shop.ID] = shop
	}
	ordered := make([]model.Shop, 0, len(shops))
	for i, id := range ids {
		shop, ok := byID[id]
		if !ok || shop.TypeID != typeID {
			continue
		}
		distance := items[i].Dist
		shop.Distance = &distance
		ordered = append(ordered, shop)
	}
	return ordered, nil
}

func (s *ShopService) Types(ctx context.Context) ([]model.ShopType, error) {
	keys := cache.Keys{Data: redisx.ShopTypeKey, Lock: redisx.ShopTypeLockKey, Version: redisx.ShopTypeVersionKey}
	data, err := s.cache.Get(ctx, keys, func(ctx context.Context) ([]byte, error) {
		types, err := s.repo.ShopTypes(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(types)
	})
	if err != nil {
		return nil, err
	}
	types := make([]model.ShopType, 0)
	if err = json.Unmarshal(data, &types); err != nil {
		return nil, err
	}
	return types, nil
}

func (s *ShopService) syncGeo(ctx context.Context, old, next *model.Shop) error {
	pipe := s.rdb.TxPipeline()
	if old != nil && old.TypeID != next.TypeID {
		pipe.ZRem(ctx, redisx.Key(redisx.ShopGeoPrefix, old.TypeID), strconv.FormatInt(old.ID, 10))
	}
	pipe.GeoAdd(ctx, redisx.Key(redisx.ShopGeoPrefix, next.TypeID), &redis.GeoLocation{Name: strconv.FormatInt(next.ID, 10), Longitude: next.X, Latitude: next.Y})
	_, err := pipe.Exec(ctx)
	return err
}

func (s *ShopService) geoFailed(ctx context.Context, cause error) {
	s.log.Warn("GEO write failed; index will be rebuilt", zap.Error(cause))
	if err := s.rdb.Del(ctx, redisx.ShopGeoReadyKey).Err(); err != nil {
		s.log.Warn("GEO readiness invalidation failed", zap.Error(err))
	}
}

func (s *ShopService) WarmGeo(ctx context.Context) error {
	s.geoMu.Lock()
	defer s.geoMu.Unlock()
	shops, err := s.repo.ShopAll(ctx)
	if err != nil {
		return err
	}
	groups := make(map[int64][]*redis.GeoLocation)
	for _, shop := range shops {
		if !validCoordinates(shop.X, shop.Y) {
			return fmt.Errorf("shop %d has invalid GEO coordinates", shop.ID)
		}
		groups[shop.TypeID] = append(groups[shop.TypeID], &redis.GeoLocation{Name: strconv.FormatInt(shop.ID, 10), Longitude: shop.X, Latitude: shop.Y})
	}
	// GEOADD is idempotent. We do not delete shared indexes while other replicas read them.
	pipe := s.rdb.TxPipeline()
	for typeID, locations := range groups {
		pipe.GeoAdd(ctx, redisx.Key(redisx.ShopGeoPrefix, typeID), locations...)
	}
	pipe.Set(ctx, redisx.ShopGeoReadyKey, "1", 10*time.Minute)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *ShopService) Close() { s.cache.Close() }

// shopStore exposes only the persistence operations this consumer needs.
type shopStore interface {
	ShopGet(ctx context.Context, id int64) (*model.Shop, error)
	ShopCreate(ctx context.Context, shop *model.Shop) error
	ShopUpdate(ctx context.Context, id int64, input dto.ShopInput) error
	ShopListByType(ctx context.Context, typeID int64, page, size int) ([]model.Shop, error)
	ShopListByName(ctx context.Context, name string, page, size int) ([]model.Shop, error)
	ShopListByIDs(ctx context.Context, ids []int64) ([]model.Shop, error)
	ShopAll(ctx context.Context) ([]model.Shop, error)
	ShopTypes(ctx context.Context) ([]model.ShopType, error)
	ShopTypeExists(ctx context.Context, id int64) (bool, error)
}
