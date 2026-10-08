package study

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

var studyZone = time.FixedZone("Asia/Shanghai", 8*60*60)
var coreModules = []string{"政治理论", "常识判断", "言语理解与表达", "数量关系", "判断推理", "资料分析"}

type StudyCounts struct {
	Answered   int      `json:"answered"`
	Correct    int      `json:"correct"`
	Accuracy   *float64 `json:"accuracy"`
	DurationMS int64    `json:"duration_ms"`
	Sessions   int      `json:"sessions"`
}
type StudyDay struct {
	Date string `json:"date"`
	StudyCounts
}
type StudyModule struct {
	Module     string `json:"module"`
	Sufficient bool   `json:"sufficient"`
	StudyCounts
}
type StudyStreak struct {
	Current        int  `json:"current"`
	Longest        int  `json:"longest"`
	PracticedToday bool `json:"practiced_today"`
}
type Achievement struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Current  int    `json:"current"`
	Target   int    `json:"target"`
	Unlocked bool   `json:"unlocked"`
}
type Dashboard struct {
	Timezone     string            `json:"timezone"`
	StartDate    string            `json:"start_date"`
	EndDate      string            `json:"end_date"`
	Days         int               `json:"days"`
	Profile      Profile           `json:"profile"`
	Lifetime     StudyCounts       `json:"lifetime"`
	Period       StudyCounts       `json:"period"`
	Previous     StudyCounts       `json:"previous"`
	Today        StudyCounts       `json:"today"`
	Trend        []StudyDay        `json:"trend"`
	Modules      []StudyModule     `json:"modules"`
	Activity     []StudyDay        `json:"activity"`
	Streak       StudyStreak       `json:"streak"`
	Achievements []Achievement     `json:"achievements"`
	Review       ReviewSuggestions `json:"review"`
}

func accuracy(answered, correct int) *float64 {
	if answered == 0 {
		return nil
	}
	value := float64(correct) * 100 / float64(answered)
	return &value
}
func (c *StudyCounts) add(v StudyCounts) {
	c.Answered += v.Answered
	c.Correct += v.Correct
	c.DurationMS += v.DurationMS
	c.Sessions += v.Sessions
	c.Accuracy = accuracy(c.Answered, c.Correct)
}
func studyToday(now time.Time) time.Time {
	now = now.In(studyZone)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, studyZone)
}
func studyWindow(now time.Time, days int) (time.Time, time.Time) {
	today := studyToday(now)
	return today.AddDate(0, 0, 1-days), today.AddDate(0, 0, 1)
}
func validStudyDays(days int) bool { return days == 7 || days == 30 || days == 90 }

// Dashboard uses a single read snapshot. ReadOnly is required because store.Open
// configures immediate write transactions; analysis must not hold their lock.
func (s *Service) Dashboard(ctx context.Context, user int64, days int) (Dashboard, error) {
	out := Dashboard{Timezone: "Asia/Shanghai", Days: days, Trend: []StudyDay{}, Modules: []StudyModule{}, Activity: []StudyDay{}, Achievements: []Achievement{}}
	if !validStudyDays(days) {
		return out, ErrInvalid
	}
	start, end := studyWindow(s.clockNow(), days)
	today := end.AddDate(0, 0, -1)
	out.StartDate, out.EndDate = start.Format("2006-01-02"), today.Format("2006-01-02")
	previousStart := start.AddDate(0, 0, -days).Format("2006-01-02")
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Profile, err = readProfile(ctx, tx, user)
	if err != nil {
		return out, err
	}
	// Answers are reduced to one row per session before joining durations.
	// Date grouping also supplies all historical activity days for longest streak.
	rows, err := tx.QueryContext(ctx, `WITH counts AS (
 SELECT a.session_id, SUM(CASE WHEN trim(a.user_answer)<>'' THEN 1 ELSE 0 END) answered,
 SUM(CASE WHEN trim(a.user_answer)<>'' AND a.is_correct=1 THEN 1 ELSE 0 END) correct
 FROM practice_answer a JOIN practice_session p ON p.id=a.session_id
 WHERE p.user_id=? AND p.submitted_at IS NOT NULL GROUP BY a.session_id)
 SELECT date(p.submitted_at,'+8 hours'),COALESCE(SUM(c.answered),0),COALESCE(SUM(c.correct),0),SUM(p.duration_ms),COUNT(*)
 FROM practice_session p LEFT JOIN counts c ON c.session_id=p.id
 WHERE p.user_id=? AND p.submitted_at IS NOT NULL AND julianday(p.submitted_at)<julianday(?)
 GROUP BY date(p.submitted_at,'+8 hours') ORDER BY 1`, user, user, end.UTC().Format(time.RFC3339))
	if err != nil {
		return out, err
	}
	daily := map[string]StudyCounts{}
	active := []string{}
	for rows.Next() {
		var day string
		var c StudyCounts
		if err = rows.Scan(&day, &c.Answered, &c.Correct, &c.DurationMS, &c.Sessions); err != nil {
			rows.Close()
			return out, err
		}
		c.Accuracy = accuracy(c.Answered, c.Correct)
		daily[day] = c
		out.Lifetime.add(c)
		if day >= out.StartDate {
			out.Period.add(c)
		} else if day >= previousStart {
			out.Previous.add(c)
		}
		if c.Answered > 0 {
			active = append(active, day)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Today = daily[out.EndDate]
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		out.Trend = append(out.Trend, StudyDay{Date: date, StudyCounts: daily[date]})
	}
	for i := 89; i >= 0; i-- {
		date := today.AddDate(0, 0, -i).Format("2006-01-02")
		out.Activity = append(out.Activity, StudyDay{Date: date, StudyCounts: daily[date]})
	}
	out.Streak = historicalStreak(active, today)
	out.Modules, err = dashboardModules(ctx, tx, user, start, end)
	if err != nil {
		return out, err
	}
	out.Review, err = dashboardReview(ctx, tx, user, start, end, out.Period, out.Modules)
	if err != nil {
		return out, err
	}
	// The six-module milestone is cumulative, independent of the selected window.
	rows, err = tx.QueryContext(ctx, `SELECT COALESCE(q.module,''),COUNT(*) FROM practice_answer a JOIN practice_session p ON p.id=a.session_id JOIN question q ON q.id=a.question_id WHERE p.user_id=? AND p.submitted_at IS NOT NULL AND julianday(p.submitted_at)<julianday(?) AND trim(a.user_answer)<>'' GROUP BY COALESCE(q.module,'')`, user, end.UTC().Format(time.RFC3339))
	if err != nil {
		return out, err
	}
	moduleTotals := map[string]int{}
	for rows.Next() {
		var module string
		var count int
		if err = rows.Scan(&module, &count); err != nil {
			rows.Close()
			return out, err
		}
		moduleTotals[module] = count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	completedModules := 0
	for _, module := range coreModules {
		if moduleTotals[module] >= 5 {
			completedModules++
		}
	}
	for _, a := range []Achievement{
		{ID: "first_answer", Name: "首次作答", Current: min(out.Lifetime.Answered, 1), Target: 1},
		{ID: "answers_100", Name: "累计 100 次作答", Current: out.Lifetime.Answered, Target: 100},
		{ID: "answers_1000", Name: "累计 1,000 次作答", Current: out.Lifetime.Answered, Target: 1000},
		{ID: "streak_7", Name: "连续练习 7 天", Current: out.Streak.Longest, Target: 7},
		{ID: "streak_30", Name: "连续练习 30 天", Current: out.Streak.Longest, Target: 30},
		{ID: "six_modules", Name: "六模块各作答 5 次", Current: completedModules, Target: 6},
	} {
		a.Unlocked = a.Current >= a.Target
		out.Achievements = append(out.Achievements, a)
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

func historicalStreak(active []string, today time.Time) StudyStreak {
	out := StudyStreak{}
	run := 0
	previous := ""
	last := ""
	for _, day := range active {
		date, _ := time.ParseInLocation("2006-01-02", day, studyZone)
		if date.AddDate(0, 0, -1).Format("2006-01-02") == previous {
			run++
		} else {
			run = 1
		}
		out.Longest = max(out.Longest, run)
		previous = day
		last = day
	}
	out.PracticedToday = last == today.Format("2006-01-02")
	if out.PracticedToday || last == today.AddDate(0, 0, -1).Format("2006-01-02") {
		out.Current = run
	}
	return out
}

func dashboardModules(ctx context.Context, tx *sql.Tx, user int64, start, end time.Time) ([]StudyModule, error) {
	modules := map[string]StudyCounts{}
	// Missing modules remain in overall totals, but cannot be actionable module
	// filters. Do not invent a core classification or an empty-module suggestion.
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT COALESCE(module,'') AS module FROM question WHERE COALESCE(module,'')<>'' ORDER BY module`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		modules[name] = StudyCounts{}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT COALESCE(q.module,''),COUNT(*),SUM(CASE WHEN a.is_correct=1 THEN 1 ELSE 0 END),SUM(a.duration_ms),COUNT(DISTINCT p.id)
 FROM practice_answer a JOIN practice_session p ON p.id=a.session_id JOIN question q ON q.id=a.question_id
 WHERE p.user_id=? AND p.submitted_at IS NOT NULL AND julianday(p.submitted_at)>=julianday(?) AND julianday(p.submitted_at)<julianday(?) AND trim(a.user_answer)<>'' AND COALESCE(q.module,'')<>'' GROUP BY COALESCE(q.module,'')`, user, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		var c StudyCounts
		if err = rows.Scan(&name, &c.Answered, &c.Correct, &c.DurationMS, &c.Sessions); err != nil {
			rows.Close()
			return nil, err
		}
		c.Accuracy = accuracy(c.Answered, c.Correct)
		modules[name] = c
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	ordered := append([]string{}, coreModules...)
	for _, name := range coreModules {
		if _, ok := modules[name]; !ok {
			modules[name] = StudyCounts{}
		}
	}
	extra := []string{}
	for name := range modules {
		found := false
		for _, core := range coreModules {
			if name == core {
				found = true
			}
		}
		if !found {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	ordered = append(ordered, extra...)
	out := make([]StudyModule, 0, len(ordered))
	for _, name := range ordered {
		c := modules[name]
		out = append(out, StudyModule{Module: name, Sufficient: c.Answered >= 5, StudyCounts: c})
	}
	return out, nil
}
