package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/learning/go-dianping/internal/config"
	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/redisx"
	"github.com/learning/go-dianping/pkg/apperror"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"math/big"
	"regexp"
	"strconv"
	"time"
)

var phonePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)

func ValidPhone(phone string) bool { return phonePattern.MatchString(phone) }

type UserService struct {
	repo     userStore
	redis    *redis.Client
	log      *zap.Logger
	cfg      config.Config
	location *time.Location
	now      func() time.Time
}

func NewUser(repo userStore, rdb *redis.Client, log *zap.Logger, cfg config.Config) *UserService {
	loc, _ := time.LoadLocation(cfg.Timezone)
	return &UserService{repo: repo, redis: rdb, log: log, cfg: cfg, location: loc, now: time.Now}
}

func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

var sendCodeScript = redis.NewScript(`
if redis.call('EXISTS',KEYS[2]) == 1 then return 0 end
redis.call('SET',KEYS[1],ARGV[1],'PX',ARGV[2])
redis.call('SET',KEYS[2],'1','PX',ARGV[3])
return 1`)

func (s *UserService) SendCode(ctx context.Context, phone string) error {
	if !ValidPhone(phone) {
		return apperror.BadRequest("手机号格式错误")
	}
	if !s.cfg.Auth.DevCodeLog {
		return apperror.Unavailable("短信发送服务尚未配置")
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	result, err := sendCodeScript.Run(ctx, s.redis, []string{redisx.LoginCodePrefix + phone, redisx.LoginThrottlePrefix + phone}, code, s.cfg.Auth.CodeTTL.Milliseconds(), s.cfg.Auth.CodeInterval.Milliseconds()).Int()
	if err != nil {
		return fmt.Errorf("保存验证码: %w", err)
	}
	if result == 0 {
		return apperror.BadRequest("验证码发送频繁，请稍后重试")
	}
	// 教学环境用日志模拟短信；生产模式明确禁用，不假装已接入短信商。
	s.log.Info("开发环境短信验证码", zap.String("phone_suffix", phone[len(phone)-4:]), zap.String("code", code))
	return nil
}

var verifyCodeScript = redis.NewScript(`
local tries=redis.call('INCR',KEYS[2])
if tries==1 then redis.call('PEXPIRE',KEYS[2],ARGV[2]) end
if tries>5 then return -1 end
local code=redis.call('GET',KEYS[1])
if not code or code~=ARGV[1] then return 0 end
redis.call('DEL',KEYS[1])
redis.call('DEL',KEYS[2])
return 1`)

func (s *UserService) Login(ctx context.Context, form dto.Login) (string, error) {
	if !ValidPhone(form.Phone) {
		return "", apperror.BadRequest("手机号格式错误")
	}
	var user *model.User
	var err error
	if form.Code != "" {
		result, e := verifyCodeScript.Run(ctx, s.redis, []string{redisx.LoginCodePrefix + form.Phone, redisx.LoginAttemptsPrefix + form.Phone}, form.Code, s.cfg.Auth.CodeTTL.Milliseconds()).Int()
		if e != nil {
			return "", e
		}
		if result != 1 {
			return "", apperror.BadRequest("验证码无效或尝试次数过多")
		}
		nickname, e := RandomToken()
		if e != nil {
			return "", e
		}
		user, err = s.repo.FindOrCreateUser(ctx, form.Phone, "user_"+nickname[:10])
	} else if form.Password != "" {
		key := redisx.LoginAttemptsPrefix + "password:" + form.Phone
		n, e := s.redis.Incr(ctx, key).Result()
		if e != nil {
			return "", e
		}
		if n == 1 {
			if e = s.redis.Expire(ctx, key, 2*time.Minute).Err(); e != nil {
				return "", e
			}
		}
		if n > 5 {
			return "", apperror.BadRequest("密码尝试次数过多")
		}
		user, err = s.repo.UserByPhone(ctx, form.Phone)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", apperror.BadRequest("手机号或密码错误")
		}
		if err != nil {
			return "", err
		}
		if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(form.Password)) != nil {
			return "", apperror.BadRequest("手机号或密码错误")
		}
		if e = s.redis.Del(ctx, key).Err(); e != nil {
			return "", e
		}
	} else {
		return "", apperror.BadRequest("请提供验证码或密码")
	}
	if err != nil {
		return "", err
	}
	token, err := RandomToken()
	if err != nil {
		return "", err
	}
	_, err = s.redis.TxPipelined(ctx, func(p redis.Pipeliner) error {
		key := redisx.LoginTokenPrefix + token
		p.HSet(ctx, key, "id", strconv.FormatInt(user.ID, 10), "nickName", user.NickName, "icon", user.Icon)
		p.Expire(ctx, key, s.cfg.Auth.SessionTTL)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("保存登录态: %w", err)
	}
	return token, nil
}

var refreshSessionScript = redis.NewScript(`
if redis.call('EXISTS',KEYS[1])==0 then return {} end
local user=redis.call('HGETALL',KEYS[1])
redis.call('PEXPIRE',KEYS[1],ARGV[1])
return user`)

func (s *UserService) Session(ctx context.Context, token string) (*dto.User, error) {
	if token == "" || len(token) > 512 {
		return nil, nil
	}
	values, err := refreshSessionScript.Run(ctx, s.redis, []string{redisx.LoginTokenPrefix + token}, s.cfg.Auth.SessionTTL.Milliseconds()).StringSlice()
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	fields := map[string]string{}
	for i := 0; i+1 < len(values); i += 2 {
		fields[values[i]] = values[i+1]
	}
	id, err := strconv.ParseInt(fields["id"], 10, 64)
	if err != nil || id <= 0 {
		return nil, apperror.ErrUnauthorized
	}
	return &dto.User{ID: id, NickName: fields["nickName"], Icon: fields["icon"]}, nil
}
func (s *UserService) Logout(ctx context.Context, token string) error {
	return s.redis.Del(ctx, redisx.LoginTokenPrefix+token).Err()
}
func (s *UserService) ByID(ctx context.Context, id int64) (*dto.User, error) {
	u, err := s.repo.UserByID(ctx, id)
	if errors.Is(err, apperror.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &dto.User{ID: u.ID, NickName: u.NickName, Icon: u.Icon}, nil
}
func (s *UserService) Info(ctx context.Context, id int64) (*model.UserInfo, error) {
	return s.repo.UserInfo(ctx, id)
}
func (s *UserService) SetPassword(ctx context.Context, id int64, password string) error {
	if len(password) < 8 || len(password) > 72 {
		return apperror.BadRequest("密码长度须为 8–72 字节")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.repo.SetPassword(ctx, id, string(hash))
}
func (s *UserService) Sign(ctx context.Context, id int64) error {
	now := s.now().In(s.location)
	key := redisx.Key(redisx.SignPrefix, id) + now.Format(":200601")
	return s.redis.SetBit(ctx, key, int64(now.Day()-1), 1).Err()
}
func (s *UserService) SignCount(ctx context.Context, id int64) (int, error) {
	now := s.now().In(s.location)
	key := redisx.Key(redisx.SignPrefix, id) + now.Format(":200601")
	values, err := s.redis.BitField(ctx, key, "GET", fmt.Sprintf("u%d", now.Day()), 0).Result()
	if err != nil {
		return 0, err
	}
	if len(values) == 0 {
		return 0, nil
	}
	return TrailingOnes(uint64(values[0])), nil
}
func TrailingOnes(bits uint64) int {
	count := 0
	for bits&1 == 1 {
		count++
		bits >>= 1
	}
	return count
}

// userStore exposes only the persistence operations this consumer needs.
type userStore interface {
	UserByPhone(ctx context.Context, phone string) (*model.User, error)
	FindOrCreateUser(ctx context.Context, phone, nickname string) (*model.User, error)
	UserByID(ctx context.Context, id int64) (*model.User, error)
	UserInfo(ctx context.Context, id int64) (*model.UserInfo, error)
	SetPassword(ctx context.Context, id int64, password string) error
}
