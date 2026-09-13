package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/repository"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// sessionTTL 统一登录会话创建和滑动续期的有效时长。
const sessionTTL = 3000 * time.Hour

// 固定使用北京时间，避免服务器时区影响签到日期。
var signLocation = time.FixedZone("CST", 8*60*60)

var phonePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)
var ErrInvalidPhone = errors.New("手机号格式错误")
var ErrInvalidCode = errors.New("验证码无效或尝试次数过多")
var ErrCodeTooFrequent = errors.New("验证码获取频繁，请稍后重试")
var ErrInvalidUserInfo = errors.New("用户资料参数错误")

var verifyCodeScript = redis.NewScript(`
local tries=redis.call('INCR',KEYS[2])
if tries == 1 then
    redis.call('EXPIRE', KEYS[2], 120)
end
if tries > 5 then
    return 0
end

local code = redis.call('GET', KEYS[1])
if not code or code ~= ARGV[1] then
    return 0
end

redis.call('DEL', KEYS[1])
redis.call('DEL', KEYS[2])
return 1
`)

var sendCodeScript = redis.NewScript(`
if redis.call('EXISTS',KEYS[2])==1 then
		return 0
end

redis.call('SET',KEYS[1],ARGV[1],'EX',120)
redis.call('SET',KEYS[2],'1','EX',60)
return 1
`)

func ValidPhone(phone string) bool {
	return phonePattern.MatchString(phone)
}

type UserService struct {
	repo *repository.Repository
	rdb  *redis.Client
}

func NewUservice(
	repo *repository.Repository,
	rdb *redis.Client,
) *UserService {
	return &UserService{
		repo: repo,
		rdb:  rdb,
	}
}

func (s *UserService) SendCode(
	ctx context.Context,
	phone string,
) error {
	if !phonePattern.MatchString(phone) {
		return ErrInvalidPhone
	}

	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	result, err := sendCodeScript.Run(
		ctx,
		s.rdb,
		[]string{
			"practice:login:code:" + phone,
			"practice:login:throttle:" + phone,
		},
		code,
	).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrCodeTooFrequent
	}

	log.Printf("开发验证码：手机号尾号=%s，验证码=%s", phone[len(phone)-4:], code)
	return nil
}

func (s *UserService) Login(
	ctx context.Context,
	in dto.LoginRequest,
) (string, error) {
	if !ValidPhone(in.Phone) {
		return "", ErrInvalidPhone
	}

	ok, err := verifyCodeScript.Run(
		ctx,
		s.rdb,
		[]string{
			"practice:login:code:" + in.Phone,
			"practice:login:attempts:" + in.Phone,
		},
		in.Code,
	).Int()

	if err != nil {
		return "", err
	}
	if ok != 1 {
		return "", ErrInvalidCode
	}
	user, err := s.repo.FindOrCreateUser(ctx, in.Phone, in.Phone)
	if err != nil {
		return "", err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

	session := dto.UserDTO{
		ID:       user.ID,
		NickName: user.NickName,
		Icon:     user.Icon,
	}
	data, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	key := "practice:login:token:" + token
	if err := s.rdb.Set(ctx, key, data, sessionTTL).Err(); err != nil {
		return "", err
	}
	return token, nil
}

func (s *UserService) GetSession(
	ctx context.Context,
	token string,
) (*dto.UserDTO, error) {
	if len(token) != 64 {
		return nil, nil
	}
	if _, err := hex.DecodeString(token); err != nil {
		return nil, nil
	}
	key := "practice:login:token:" + token
	data, err := s.rdb.GetEx(ctx, key, sessionTTL).Result()
	if errors.Is(err, redis.Nil) {
		return nil, err
	}
	var user dto.UserDTO
	if err := json.Unmarshal([]byte(data), &user); err != nil {
		return nil, err
	}
	if user.ID <= 0 {
		return nil, errors.New("会话中的用户ID无效")
	}
	return &user, nil
}

func (s *UserService) Logout(
	ctx context.Context,
	token string,
) error {
	key := "practice:login:token:" + token
	return s.rdb.Del(ctx, key).Err()
}

func (s *UserService) FindUser(
	ctx context.Context,
	id int64,
) (*dto.UserDTO, error) {
	user, err := s.repo.FindUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *UserService) UpdateUserInfo(
	ctx context.Context,
	id int64,
	in dto.UpdateUserInfoRequest,
) error {
	updates := make(map[string]any)
	if in.City != nil {
		city := strings.TrimSpace(*in.City)
		if utf8.RuneCountInString(city) > 64 {
			return fmt.Errorf("%w：城市不能超过64个字符", ErrInvalidUserInfo)
		}
		updates["city"] = city
	}
	if in.Introduce != nil {
		introduce := strings.TrimSpace(*in.Introduce)
		if utf8.RuneCountInString(introduce) > 255 {
			return fmt.Errorf("%w：简介不能超过255个字符", ErrInvalidUserInfo)
		}
		updates["introduce"] = introduce
	}
	if in.Birthday != nil {
		birthday := strings.TrimSpace(*in.Birthday)

		if birthday == "" {
			// SQL NULL，表示清空生日。
			updates["birthday"] = nil
		} else {
			date, err := time.Parse("2006-01-02", birthday)
			if err != nil || date.Year() < 1000 {
				return fmt.Errorf(
					"%w：生日须为有效日期，格式为YYYY-MM-DD",
					ErrInvalidUserInfo,
				)
			}

			// 使用中国标准时间的日期判断是否晚于今天。
			today := time.Now().
				In(time.FixedZone("CST", 8*60*60)).
				Format("2006-01-02")

			if birthday > today {
				return fmt.Errorf("%w：生日不能晚于今天", ErrInvalidUserInfo)
			}

			updates["birthday"] = birthday
		}
	}
	if len(updates) == 0 {
		return fmt.Errorf("%w：至少提供一个修改字段", ErrInvalidUserInfo)
	}
	if _, err := s.repo.FindUser(ctx, id); err != nil {
		return err
	}
	return s.repo.SaveUserInfo(ctx, id, updates)
}

func (s *UserService) UserInfo(
	ctx context.Context,
	id int64,
) (*model.UserInfo, error) {
	user, err := s.repo.UserInfo(ctx, id)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *UserService) UpdateUserName(
	ctx context.Context,
	id int64,
	in dto.UserDTO,
) error {
	updates := make(map[string]any)
	name := strings.TrimSpace(in.NickName)

	if utf8.RuneCountInString(name) > 64 {
		return fmt.Errorf("%w: 昵称不能超过64个字符", ErrInvalidUserInfo)
	}

	updates["nick_name"] = name
	updates["icon"] = in.Icon
	if len(updates) == 0 {
		return fmt.Errorf("%w：至少提供一个修改字段", ErrInvalidUserInfo)
	}
	if _, err := s.repo.FindUser(ctx, id); err != nil {
		return err
	}
	return s.repo.SaveUserName(ctx, id, updates)
}

func (s *UserService) SetPassword(
	ctx context.Context,
	id int64,
	in dto.Password,
) error {
	if len(in.Password) < 8 || len(in.Password) > 72 {
		return errors.New("密码长度必须为 8～72 字节")
	}
	hash, err := bcrypt.GenerateFromPassword(
		[]byte(in.Password),
		bcrypt.DefaultCost,
	)
	if err != nil {
		return err
	}

	return s.repo.SetPassword(ctx, id, string(hash))
}

func (s *UserService) Sign(
	ctx context.Context,
	id int64,
) error {
	now := time.Now().In(signLocation)
	key := fmt.Sprintf(
		"practice:sign:%d:%s",
		id,
		now.Format("200601"),
	)
	offset := int64(now.Day() - 1)
	return s.rdb.SetBit(ctx, key, offset, 1).Err()
}

func (s *UserService) SignCount(
	ctx context.Context,
	id int64,
) (int, error) {
	now := time.Now().In(signLocation)
	key := fmt.Sprintf(
		"practice:sign:%d:%s",
		id,
		now.Format("200601"),
	)
	values, err := s.rdb.BitField(
		ctx,
		key,
		"GET",
		fmt.Sprintf("u%d", now.Day()),
		0,
	).Result()
	if err != nil {
		return 0, err
	}
	if len(values) == 0 {
		return 0, nil
	}

	bits := values[0]
	count := 0

	// 最低位对应今天，向前逐天检查。
	for bits&1 == 1 {
		count++
		bits >>= 1
	}

	return count, nil
}
