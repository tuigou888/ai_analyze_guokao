package study

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"ai_analyze_guokao/internal/store"
)

var ErrProfileConflict = errors.New("资料已在其他窗口更新，请重新加载后保存")

type Profile struct {
	Username       string `json:"username"`
	Nickname       string `json:"nickname"`
	Bio            string `json:"bio"`
	AvatarID       int    `json:"avatar_id"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	DailyQuestions int    `json:"daily_questions"`
	DailyMinutes   int    `json:"daily_minutes"`
	ExamName       string `json:"exam_name"`
	ExamDate       string `json:"exam_date"`
	DefaultLimit   int    `json:"default_limit"`
	DefaultModule  string `json:"default_module"`
	ReadingSize    int    `json:"reading_size"`
	Revision       int64  `json:"revision"`
}

type ProfileUpdate struct {
	Nickname       string `json:"nickname"`
	Bio            string `json:"bio"`
	AvatarID       int    `json:"avatar_id"`
	DailyQuestions int    `json:"daily_questions"`
	DailyMinutes   int    `json:"daily_minutes"`
	ExamName       string `json:"exam_name"`
	ExamDate       string `json:"exam_date"`
	DefaultLimit   int    `json:"default_limit"`
	DefaultModule  string `json:"default_module"`
	ReadingSize    int    `json:"reading_size"`
	Revision       int64  `json:"revision"`
}

func (s *Service) clockNow() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

type profileReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readProfile(ctx context.Context, db profileReader, user int64) (Profile, error) {
	var p Profile
	err := db.QueryRowContext(ctx, `SELECT u.username,u.nickname,u.created_at,
 COALESCE(p.bio,''),COALESCE(p.avatar_id,0),COALESCE(p.daily_questions,20),COALESCE(p.daily_minutes,30),
 COALESCE(p.exam_name,''),COALESCE(p.exam_date,''),COALESCE(p.default_limit,20),COALESCE(p.default_module,''),
 COALESCE(p.reading_size,16),COALESCE(p.revision,0),COALESCE(p.updated_at,u.created_at)
 FROM app_user u LEFT JOIN user_profile p ON p.user_id=u.id WHERE u.id=?`, user).Scan(&p.Username, &p.Nickname, &p.CreatedAt, &p.Bio, &p.AvatarID, &p.DailyQuestions, &p.DailyMinutes, &p.ExamName, &p.ExamDate, &p.DefaultLimit, &p.DefaultModule, &p.ReadingSize, &p.Revision, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return p, err
}

func (s *Service) Profile(ctx context.Context, user int64) (Profile, error) {
	return readProfile(ctx, s.DB, user)
}

func (s *Service) validateProfile(ctx context.Context, in ProfileUpdate) error {
	if !utf8.ValidString(in.Nickname) || !utf8.ValidString(in.Bio) || !utf8.ValidString(in.ExamName) || utf8.RuneCountInString(in.Nickname) > 40 || utf8.RuneCountInString(in.Bio) > 200 || utf8.RuneCountInString(in.ExamName) > 40 || in.AvatarID < 0 || in.AvatarID > 7 || in.DailyQuestions < 5 || in.DailyQuestions > 200 || in.DailyMinutes < 5 || in.DailyMinutes > 180 || in.Revision < 0 || (in.DefaultLimit != 10 && in.DefaultLimit != 20 && in.DefaultLimit != 50) || (in.ReadingSize != 16 && in.ReadingSize != 18 && in.ReadingSize != 20) {
		return ErrInvalid
	}
	if in.DefaultModule != "" {
		var exists bool
		if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM question WHERE module=?)`, in.DefaultModule).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrInvalid
		}
	}
	return nil
}

func (s *Service) validateExamDate(value, original string) error {
	if value == "" {
		return nil
	}
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	date, err := time.ParseInLocation("2006-01-02", value, zone)
	now := s.clockNow().In(zone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	valid := err == nil && date.Format("2006-01-02") == value
	// An unchanged, already reached exam date is retained for unrelated edits.
	// original is read for this user inside the same revision-checked transaction.
	if valid && value == original && !date.After(today) {
		return nil
	}
	if !valid || !date.After(today) || date.After(today.AddDate(5, 0, 0)) {
		return errors.Join(ErrInvalid, errors.New("考试日期需为未来日期，最多五年后"))
	}
	return nil
}

func (s *Service) UpdateProfile(ctx context.Context, user int64, in ProfileUpdate) (Profile, error) {
	in.Nickname = strings.TrimSpace(in.Nickname)
	in.Bio = strings.TrimSpace(in.Bio)
	in.ExamName = strings.TrimSpace(in.ExamName)
	if err := s.validateProfile(ctx, in); err != nil {
		return Profile{}, err
	}
	var out Profile
	err := store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		original, err := readProfile(ctx, tx, user)
		if err != nil {
			return err
		}
		if original.Revision != in.Revision {
			return ErrProfileConflict
		}
		if err := s.validateExamDate(in.ExamDate, original.ExamDate); err != nil {
			return err
		}
		// Missing legacy profile is materialized only on an explicit write.
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_profile(user_id,updated_at) SELECT id,created_at FROM app_user WHERE id=? ON CONFLICT(user_id) DO NOTHING`, user); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE user_profile SET bio=?,avatar_id=?,daily_questions=?,daily_minutes=?,exam_name=?,exam_date=?,default_limit=?,default_module=?,reading_size=?,revision=revision+1,updated_at=? WHERE user_id=? AND revision=?`, in.Bio, in.AvatarID, in.DailyQuestions, in.DailyMinutes, in.ExamName, in.ExamDate, in.DefaultLimit, in.DefaultModule, in.ReadingSize, s.clockNow().UTC().Format(time.RFC3339), user, in.Revision)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrProfileConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE app_user SET nickname=? WHERE id=?`, in.Nickname, user); err != nil {
			return err
		}
		out, err = readProfile(ctx, tx, user)
		return err
	})
	return out, err
}
