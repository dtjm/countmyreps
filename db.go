package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
)

func totalReps(d []RepData) int {
	sum := 0
	for _, rd := range d {
		for _, count := range rd.ExerciseCounts {
			sum += count
		}
	}
	return sum
}

func populateOfficesVar(db *sql.DB) error {
	q := "SELECT name FROM office"
	rows, err := db.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var office string
		err = rows.Scan(&office)
		if err != nil {
			return err
		}
		if office != "" {
			Offices = append(Offices, office)
		}
	}
	if rows.Err() != nil {
		return rows.Err()
	}
	return nil
}

func formattedOffice(s string) string {
	for _, office := range Offices {
		if strings.ToLower(office) == strings.TrimSpace(strings.ToLower(s)) {
			return office
		}
	}
	logError(nil, fmt.Errorf("unable to determine office"), fmt.Sprintf("attempting to set office to %q", s))
	return "Unknown"
}

func inListCaseInsenitive(s string, list []string) bool {
	s = strings.ToLower(s)
	s = strings.TrimSpace(s)
	for _, elem := range list {
		if s == strings.ToLower(elem) {
			return true
		}
	}
	return false
}

func getOrCreateUserID(db *sql.DB, email string) (int, error) {
	var id int
	getQ := "SELECT id FROM user WHERE email=? LIMIT 1"
	row := db.QueryRow(getQ, email)
	err := row.Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return 0, errors.Wrap(err, queryPrinter(getQ, email))
	} else if err == sql.ErrNoRows {
		q := "INSERT INTO user (email, office) VALUES (?, (SELECT id from office where name=\"\"))"
		res, err := db.Exec(q, email)
		if err != nil {
			return 0, errors.Wrap(err, queryPrinter(q, email))
		}
		i, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		id = int(i)
		logEvent(nil, "new_user", email)
	}
	return id, nil
}

// TODO: expand the office table to have timezone info
// Alternative: don't use NOW(), use unix timestamp
func timezoneShift(office string) time.Duration {
	// timezone on server is set to my local America/Los_Angeles
	switch office {
	case "Denver":
		return time.Hour * 1
	case "Romania":
		return time.Hour * 9
	case "NY":
		return time.Hour * 3
	case "London":
		return time.Hour * 7
	}

	return time.Duration(0)
}

// getTodaysReps will only grab the latest N submissions
func getTodaysReps(db *sql.DB, email string) []RepData {
	var rd []RepData
	limit := 11
	q := fmt.Sprintf("SELECT reps.exercise, reps.count, reps.created_at, office.name FROM reps JOIN user on reps.user_id=user.id JOIN office on user.office=office.id WHERE user.email=? AND created_at >= ? ORDER BY created_at DESC LIMIT %d", limit)
	rows, err := db.Query(q, email, fmt.Sprintf("%d-%d-%d", time.Now().Year(), int(time.Now().Month()), time.Now().Day()))
	logDebug(nil, queryPrinter(q, email, fmt.Sprintf("%d-%d-%d", time.Now().Year(), int(time.Now().Month()), time.Now().Day())))
	if err != nil {
		logError(nil, errors.Wrap(err, queryPrinter(q, email, fmt.Sprintf("%d-%d-%d", time.Now().Year(), int(time.Now().Month()), time.Now().Day()))), "unable to get today's reps")
		return rd
	}
	defer rows.Close()

	for rows.Next() {
		var exercise string
		var count int
		var createdAt time.Time
		var office string
		err := rows.Scan(&exercise, &count, &createdAt, &office)
		if err != nil {
			logError(nil, errors.Wrap(err, queryPrinter(q, email, fmt.Sprintf("%d-%d-%d", time.Now().Year(), int(time.Now().Month()), time.Now().Day()))), "unable to scan today's reps")
		}
		rd = append(rd, RepData{
			Date:           createdAt.Add(timezoneShift(office)).Format(time.Kitchen),
			ExerciseCounts: map[string]int{exercise: count},
		})
	}
	if rows.Err() != nil {
		logError(nil, err, "error after rows.Next in getTodaysReps")
	}
	// reverse the data for presentation needs
	for i, j := 0, len(rd)-1; i < j; i, j = i+1, j-1 {
		rd[i], rd[j] = rd[j], rd[i]
	}
	return rd
}

func getUserOffice(db *sql.DB, email string) string {
	// leverage the empty value; there is a "" value in the office table
	var officeName string
	q := "SELECT office.name FROM user JOIN office ON user.office=office.id WHERE user.email=?"
	row := db.QueryRow(q, email)
	err := row.Scan(&officeName)
	if err != nil && err != sql.ErrNoRows {
		logError(nil, errors.Wrap(err, queryPrinter(q, email)), "unable to query for office name")
		return ""
	}
	return officeName
}

func getUserTeams(db *sql.DB, email string) []string {
	var teams []string
	q := "SELECT team.name FROM team WHERE team.id in (SELECT user_team.team_id FROM user_team JOIN user ON user_team.user_id=user.id WHERE user.email=?);"
	rows, err := db.Query(q, email)
	if err != nil {
		logError(nil, errors.Wrap(err, queryPrinter(q, email)), "unable to query for user teams")
		return teams
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		err := rows.Scan(&name)
		if err != nil {
			logError(nil, errors.Wrap(err, queryPrinter(q, email)), "unable to scan query for user teams")
			return teams
		}
		teams = append(teams, name)
	}
	if rows.Err() != nil {
		logError(nil, err, "error after rows.Next in getUserTeams")
	}

	return teams
}

func getTeamID(db *sql.DB, teamName string, createIfMissing bool) (int, error) {
	var id int
	teamName = strings.TrimSpace(teamName)
	q := "SELECT id FROM team WHERE name=? LIMIT 1"
	rows, err := db.Query(q, teamName)
	if err != nil {
		return 0, errors.Wrapf(err, "unable to scan team id for %q", teamName)
	}
	defer rows.Close()

	for rows.Next() {
		err = rows.Scan(&id)
		if err != nil {
			continue
		}

		return id, nil
	}
	if rows.Err() != nil {
		logError(nil, err, "error after rows.Next in getTeamID")
	}

	if id == 0 && createIfMissing {
		res, err := db.Exec("INSERT INTO team SET name=?", teamName)
		if err != nil {
			return 0, errors.Wrapf(err, "unable to insert team id for %q", teamName)
		}
		resID, err := res.LastInsertId()
		if err != nil {
			return 0, errors.Wrapf(err, "unable to get insert team id for %q", teamName)
		}
		id = int(resID)
		return id, nil
	}
	return 0, sql.ErrNoRows
}

func addTeam(db *sql.DB, teamName string, userID int) error {
	teamName = strings.TrimSpace(teamName)
	teamID, err := getTeamID(db, teamName, true)
	if err != nil {
		return err
	}

	if isOnTeam(db, teamName, userID) {
		return nil
	}

	q := "INSERT INTO user_team (user_id, team_id) VALUES (?,?)"
	_, err = db.Exec(q, userID, teamID)
	if err != nil {
		return errors.Wrap(err, queryPrinter(q, userID, teamID))
	}

	return nil
}

func isOnTeam(db *sql.DB, teamName string, userID int) bool {
	q := "SELECT count(*) FROM user_team WHERE user_team.team_id=(SELECT id FROM team WHERE name=?) AND user_team.user_id=?"
	rows, err := db.Query(q, teamName, userID)
	if err != nil {
		logError(nil, errors.Wrap(err, queryPrinter(q, teamName, userID)), "unable to check for team membership")
		return false
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		err = rows.Scan(&count)
		if err != nil {
			logError(nil, err, "unable to scan count for isOnTeam")
		}
	}
	if rows.Err() != nil {
		logError(nil, err, "error after rows.Next in isOnTeam")
	}
	return count > 0
}

func removeTeam(db *sql.DB, teamName string, userID int) error {
	teamName = strings.TrimSpace(teamName)
	teamID, err := getTeamID(db, teamName, false)
	if err != nil {
		return err
	}
	q := "DELETE FROM user_team WHERE user_id=? AND team_id=?"
	_, err = db.Exec(q, userID, teamID)
	if err != nil {
		return errors.Wrap(err, queryPrinter(q, userID, teamID))
	}

	return nil
}

func getTeamStats(db *sql.DB) map[string]Stats {
	teamStats := make(map[string]Stats)
	teams := getTeams(db)
	for _, team := range teams {
		teamName := team.name
		teamID := team.id
		var participating int // does not make sense for teams really; you are registered for the team otherwise you would not get a stat for it
		var headCount int
		var totalReps sql.NullInt64

		qHeadCount := "SELECT count(*) FROM user_team WHERE user_team.team_id=?"
		row := db.QueryRow(qHeadCount, teamID)
		err := row.Scan(&headCount)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			logError(nil, errors.Wrap(err, queryPrinter(qHeadCount, teamName)), "unable to scan for team head count")
		}

		// TODO: is there a better way to measure team participation, or does that not make sense? Works for offices, not so much teams.
		participating = headCount

		qTotals := "select sum(reps.count) from reps where reps.created_at > ? and reps.created_at < ? and reps.user_id in (SELECT DISTINCT user_id FROM user_team WHERE team_id=?)"
		row = db.QueryRow(qTotals, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"), teamID)
		err = row.Scan(&totalReps)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			logError(nil, errors.Wrap(err, queryPrinter(qTotals, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"), teamID)), "unable to scan for office totals")
			return teamStats
		}

		totalDays := int(EndDate.Sub(StartDate).Hours() / float64(24))
		if totalDays <= 0 {
			totalDays = 1 // avoid divide by zero
		}

		if headCount == 0 {
			headCount = 1 // avoid divide by zero
		}
		stats := Stats{}
		stats.HeadCount = headCount
		stats.TotalReps = int(totalReps.Int64)
		stats.PercentParticipating = participating * 100 / headCount
		stats.RepsPerPerson = int(totalReps.Int64) / headCount

		if participating == 0 {
			participating = 1 // avoid divide by zero
		}
		stats.RepsPerPersonParticipating = int(totalReps.Int64) / participating
		stats.RepsPerPersonParticipatingPerDay = int(totalReps.Int64) / participating / totalDays
		stats.RepsPerPersonPerDay = int(totalReps.Int64) / headCount / totalDays

		teamStats[teamName] = stats
	}
	return teamStats
}

func getOfficeStats(db *sql.DB) map[string]Stats {
	officeStats := make(map[string]Stats)
	for _, officeName := range Offices {
		var headCount int
		var participating int
		var totalReps sql.NullInt64

		qHeadCount := "SELECT head_count FROM office WHERE office.name=?"
		row := db.QueryRow(qHeadCount, officeName)
		err := row.Scan(&headCount)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			logError(nil, errors.Wrap(err, queryPrinter(qHeadCount, officeName)), "unable to scan for office head count")
		}

		qParticip := "SELECT count(distinct id) from (SELECT user.id FROM reps JOIN user on reps.user_id=user.id JOIN office ON user.office=office.id WHERE office.name=? and reps.created_at > ? AND reps.created_at < ?) participating;"
		row = db.QueryRow(qParticip, officeName, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"))
		err = row.Scan(&participating)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			logError(nil, errors.Wrap(err, queryPrinter(qParticip, officeName, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"))), "unable to scan for office participation")
			return officeStats
		}

		qTotals := "select sum(reps.count) from reps left join user on reps.user_id=user.id join office on office.id=user.office where reps.created_at > ? and reps.created_at < ? and office.name=?;"
		row = db.QueryRow(qTotals, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"), officeName)
		err = row.Scan(&totalReps)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			logError(nil, errors.Wrap(err, queryPrinter(qTotals, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"), officeName)), "unable to scan for office totals")
			return officeStats
		}

		totalDays := int(EndDate.Sub(StartDate).Hours() / float64(24))
		if totalDays <= 0 {
			totalDays = 1 // avoid divide by zero
		}

		if headCount == 0 {
			headCount = 1 // avoid divide by zero
		}
		stats := Stats{}
		stats.HeadCount = headCount
		stats.TotalReps = int(totalReps.Int64)
		stats.PercentParticipating = participating * 100 / headCount
		stats.RepsPerPerson = int(totalReps.Int64) / headCount

		if participating == 0 {
			participating = 1 // avoid divide by zero
		}
		stats.RepsPerPersonParticipating = int(totalReps.Int64) / participating
		stats.RepsPerPersonParticipatingPerDay = int(totalReps.Int64) / participating / totalDays
		stats.RepsPerPersonPerDay = int(totalReps.Int64) / headCount / totalDays

		officeStats[officeName] = stats
	}
	return officeStats
}

// Team represents the db reference to a given team to which a user can have many
type Team struct {
	id   int
	name string
}

func getTeams(db *sql.DB) []Team {
	var teams []Team
	q := "SELECT id, name FROM team"
	rows, err := db.Query(q)
	if err != nil {
		logError(nil, errors.Wrap(err, queryPrinter(q)), "unable to query teams")
		return teams
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var id int
		err := rows.Scan(&id, &name)
		if err != nil {
			logError(nil, errors.Wrap(err, queryPrinter(q)), "unable to scan teams")
		}
		teams = append(teams, Team{id: id, name: name})
	}
	if rows.Err() != nil {
		logError(nil, errors.Wrap(err, queryPrinter(q)), "post scan error for teams")
	}
	return teams
}

func getTeamReps(db *sql.DB) map[string][]RepData {
	trd := make(map[string][]RepData)
	teams := getTeams(db)
	// TODO: DRY up with getOfficeReps
	for _, team := range teams {
		q := "SELECT reps.exercise, reps.count, reps.created_at FROM reps JOIN user on reps.user_id=user.id WHERE user.id in (SELECT DISTINCT user_id FROM user_team WHERE user_team.team_id=?) AND reps.created_at > ? AND reps.created_at < ?"
		rows, err := db.Query(q, team.id, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"))
		if err != nil {
			logError(nil, errors.Wrap(err, queryPrinter(q, team.id, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"))), "unable to query for user's reps")
			return nil
		}
		defer rows.Close()

		repDatas := initRepData()
		for rows.Next() {
			var exercise string
			var count int
			var createdAt time.Time
			err = rows.Scan(&exercise, &count, &createdAt)
			if err != nil {
				logError(nil, errors.Wrap(err, queryPrinter(q, team.id, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"))), "unable to scan results for user's reps")
				return nil
			}
			for _, rd := range repDatas {
				// find which repData slot we need to populate. Probably more effecient way to do this. Probably a fancy mysql query could have done all this for me.
				if rd.Date != fmt.Sprintf("%d-%d", int(createdAt.Month()), createdAt.Day()) {
					continue
				}
				rd.ExerciseCounts[exercise] += count
			}
		}
		if rows.Err() != nil {
			logError(nil, rows.Err(), "error after parsing data for user reps")
		}
		trd[team.name] = repDatas
	}
	return trd
}

func getOfficeReps(db *sql.DB) map[string][]RepData {
	// DEBUG
	_ = getTeamReps(db)
	// END DEBUG

	officeReps := make(map[string][]RepData)
	// TODO: DRY it up getUserReps
	for _, officeName := range Offices {
		q := "SELECT reps.exercise, reps.count, reps.created_at FROM reps JOIN user on reps.user_id=user.id WHERE user.id in (SELECT user.id FROM user JOIN office on user.office=office.id WHERE office.name=?) AND reps.created_at > ? AND reps.created_at < ?"
		rows, err := db.Query(q, officeName, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"))
		if err != nil {
			logError(nil, errors.Wrap(err, queryPrinter(q, officeName, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"))), "unable to query for user's reps")
			return nil
		}
		defer rows.Close()

		repDatas := initRepData()
		for rows.Next() {
			var exercise string
			var count int
			var createdAt time.Time
			err = rows.Scan(&exercise, &count, &createdAt)
			if err != nil {
				logError(nil, errors.Wrap(err, queryPrinter(q, officeName, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"))), "unable to scan results for user's reps")
				return nil
			}
			for _, rd := range repDatas {
				// find which repData slot we need to populate. Probably more effecient way to do this. Probably a fancy mysql query could have done all this for me.
				if rd.Date != fmt.Sprintf("%d-%d", int(createdAt.Month()), createdAt.Day()) {
					continue
				}
				rd.ExerciseCounts[exercise] += count
			}
		}
		if rows.Err() != nil {
			logError(nil, rows.Err(), "error after parsing data for user reps")
		}
		officeReps[officeName] = repDatas
	}
	return officeReps
}

func getUserReps(db *sql.DB, email string) []RepData {
	q := "SELECT reps.exercise, reps.count, reps.created_at FROM reps JOIN user on reps.user_id=user.id WHERE email=? AND reps.created_at > ? AND reps.created_at < ?"
	rows, err := db.Query(q, email, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"))
	if err != nil {
		logError(nil, errors.Wrap(err, queryPrinter(q, email, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"))), "unable to query for user's reps")
		return nil
	}
	defer rows.Close()

	repDatas := initRepData()
	for rows.Next() {
		var exercise string
		var count int
		var createdAt time.Time
		err = rows.Scan(&exercise, &count, &createdAt)
		if err != nil {
			logError(nil, errors.Wrap(err, queryPrinter(q, email, StartDate.Format("2006-01-02"), EndDate.Format("2006-01-02"))), "unable to scan results for user's reps")
			return nil
		}
		for _, rd := range repDatas {
			// find which repData slot we need to populate. Probably more effecient way to do this. Probably a fancy mysql query could have done all this for me.
			if rd.Date != fmt.Sprintf("%d-%d", int(createdAt.Month()), createdAt.Day()) {
				continue
			}
			rd.ExerciseCounts[exercise] += count
		}
	}
	if rows.Err() != nil {
		logError(nil, rows.Err(), "error after parsing data for user reps")
	}
	return repDatas
}

func initRepData() []RepData {
	var rd []RepData
	for cur := StartDate; cur.Before(EndDate.Add(time.Hour * 24)); cur = cur.Add(time.Hour * 24) {
		rd = append(
			rd, RepData{
				Date:           fmt.Sprintf("%d-%d", int(cur.Month()), cur.Day()),
				ExerciseCounts: make(map[string]int),
			})
	}
	return rd
}

func queryPrinter(q string, args ...interface{}) string {
	qFmt := strings.Replace(q, "?", `"%v"`, -1)
	return fmt.Sprintf(qFmt, args...)
}
