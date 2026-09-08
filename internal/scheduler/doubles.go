package scheduler

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"keep-swinging-web/internal/session"
)

// ErrNoLineup is returned when fewer than four eligible players exist or no lineup remains after constraints.
var ErrNoLineup = errors.New("no doubles lineup available")

// Lineup is two pairs of player IDs (order within pair and swap of teams does not change LineupKey).
type Lineup struct {
	TeamA [2]string
	TeamB [2]string
}

// LineupKey returns a canonical key for comparing lineups regardless of team swap or intra-pair order.
func LineupKey(teamA, teamB [2]string) string {
	pair := func(x, y string) string {
		if x < y {
			return x + "," + y
		}
		return y + "," + x
	}
	p1 := pair(teamA[0], teamA[1])
	p2 := pair(teamB[0], teamB[1])
	if p1 > p2 {
		p1, p2 = p2, p1
	}
	return p1 + "|" + p2
}

// fairnessScore is the sum of games_played for the four players — lower means those players are more "due".
func fairnessScore(games map[string]int, lu Lineup) int {
	sum := 0
	for _, id := range lu.TeamA[:] {
		sum += games[id]
	}
	for _, id := range lu.TeamB[:] {
		sum += games[id]
	}
	return sum
}

// calculateMexicanoPoints computes individual points from recorded matches.
// Each player receives the score their team won from each match.
func calculateMexicanoPoints(matches []session.RecordedMatch) map[string]int {
	pts := make(map[string]int)
	for _, m := range matches {
		for _, id := range m.TeamAIDs {
			pts[id] += m.ScoreA
		}
		for _, id := range m.TeamBIDs {
			pts[id] += m.ScoreB
		}
	}
	return pts
}

// mexicanoScore scores lineups for mexicano style:
// 1. Prefer the 4 least-experienced players (fairness - rotate players in)
// 2. Among those, prefer splits where the GP difference between teams is maximized (strongest vs weakest)
func mexicanoScore(games map[string]int, lu Lineup) int {
	sum := 0
	for _, id := range lu.TeamA[:] {
		sum += games[id]
	}
	for _, id := range lu.TeamB[:] {
		sum += games[id]
	}

	teamA := games[lu.TeamA[0]] + games[lu.TeamA[1]]
	teamB := games[lu.TeamB[0]] + games[lu.TeamB[1]]
	diff := teamA - teamB
	if diff < 0 {
		diff = -diff
	}

	return sum*1000 - diff
}

// mexicanoTopVsTopScore scores lineups for mexicano top-vs-top style:
// 1. Prefer the 4 most-experienced players (highest games played)
// 2. Among those, prefer splits where the GP difference between teams is maximized (strongest vs weakest)
func mexicanoTopVsTopScore(games map[string]int, lu Lineup) int {
	sum := 0
	for _, id := range lu.TeamA[:] {
		sum += games[id]
	}
	for _, id := range lu.TeamB[:] {
		sum += games[id]
	}
	
	teamA := games[lu.TeamA[0]] + games[lu.TeamA[1]]
	teamB := games[lu.TeamB[0]] + games[lu.TeamB[1]]
	diff := teamA - teamB
	if diff < 0 {
		diff = -diff
	}
	
	return -sum*1000 + diff
}

// mexicanoTopVsBottomScore scores lineups for mexicano top-vs-bottom style:
// 1. Prefer the 4 least-experienced players (fairness - rotate players in)
// 2. Among those, pair strongest+weakest vs middle two (maximize GP difference)
func mexicanoTopVsBottomScore(games map[string]int, lu Lineup) int {
	sum := 0
	for _, id := range lu.TeamA[:] {
		sum += games[id]
	}
	for _, id := range lu.TeamB[:] {
		sum += games[id]
	}
	
	teamA := games[lu.TeamA[0]] + games[lu.TeamA[1]]
	teamB := games[lu.TeamB[0]] + games[lu.TeamB[1]]
	diff := teamA - teamB
	if diff < 0 {
		diff = -diff
	}
	
	return sum*1000 - diff
}

func pickMexicanoLineup(eligible []session.Player, matches []session.RecordedMatch, byID map[string]session.Player) (*session.SuggestedMatch, string, error) {
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].GamesPlayed != eligible[j].GamesPlayed {
			return eligible[i].GamesPlayed < eligible[j].GamesPlayed
		}
		return eligible[i].ID < eligible[j].ID
	})

	minGP := eligible[0].GamesPlayed
	var fairGroup []session.Player
	for _, p := range eligible {
		if p.GamesPlayed == minGP {
			fairGroup = append(fairGroup, p)
		} else {
			break
		}
	}

	if len(fairGroup) < 4 {
		for _, p := range eligible {
			if p.GamesPlayed == minGP {
				continue
			}
			fairGroup = append(fairGroup, p)
			if len(fairGroup) >= 4 {
				break
			}
		}
	}

	if len(fairGroup) < 4 {
		return nil, "", ErrNoLineup
	}

	if len(matches) == 0 {
		ids := make([]string, len(fairGroup))
		for i := range fairGroup {
			ids[i] = fairGroup[i].ID
		}
		
		var candidates []Lineup
		eachQuartet(ids, func(q [4]string) {
			for _, lu := range lineupsForQuartet(q) {
				candidates = append(candidates, lu)
			}
		})

		if len(candidates) == 0 {
			return nil, "", ErrNoLineup
		}

		games := gamesMap(fairGroup)
		var bestScore int
		for i, lu := range candidates {
			s := mexicanoScore(games, lu)
			if i == 0 || s < bestScore {
				bestScore = s
			}
		}

		var best []Lineup
		for _, lu := range candidates {
			if mexicanoScore(games, lu) == bestScore {
				best = append(best, lu)
			}
		}

		chosen := best[pickIndex(len(best))]
		key := LineupKey(chosen.TeamA, chosen.TeamB)
		
		buildTeam := func(pair [2]string) []session.Player {
			return []session.Player{byID[pair[0]], byID[pair[1]]}
		}

		return &session.SuggestedMatch{
			TeamA: buildTeam(chosen.TeamA),
			TeamB: buildTeam(chosen.TeamB),
		}, key, nil
	}

	pts := calculateMexicanoPoints(matches)
	sort.SliceStable(fairGroup, func(i, j int) bool {
		return pts[fairGroup[i].ID] > pts[fairGroup[j].ID]
	})

	selected := fairGroup[:4]
	
	lu := Lineup{
		TeamA: [2]string{selected[0].ID, selected[2].ID},
		TeamB: [2]string{selected[1].ID, selected[3].ID},
	}
	key := LineupKey(lu.TeamA, lu.TeamB)

	return &session.SuggestedMatch{
		TeamA: []session.Player{selected[0], selected[2]},
		TeamB: []session.Player{selected[1], selected[3]},
}, key, nil
}

func eachQuartet(ids []string, yield func([4]string)) {
	n := len(ids)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			for k := j + 1; k < n; k++ {
				for l := k + 1; l < n; l++ {
					yield([4]string{ids[i], ids[j], ids[k], ids[l]})
				}
			}
		}
	}
}

func lineupsForQuartet(q [4]string) []Lineup {
	a, b, c, d := q[0], q[1], q[2], q[3]
	return []Lineup{
		{TeamA: [2]string{a, b}, TeamB: [2]string{c, d}},
		{TeamA: [2]string{a, c}, TeamB: [2]string{b, d}},
		{TeamA: [2]string{a, d}, TeamB: [2]string{b, c}},
	}
}

func filterPlayers(players []session.Player, excludeIDs map[string]bool) []session.Player {
	if len(excludeIDs) == 0 {
		out := make([]session.Player, len(players))
		copy(out, players)
		return out
	}
	var out []session.Player
	for _, p := range players {
		if excludeIDs[p.ID] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// activeShufflePlayers are session members who participate in matchup selection (not inactive / away).
func activeShufflePlayers(players []session.Player) []session.Player {
	var out []session.Player
	for _, p := range players {
		if !p.Inactive {
			out = append(out, p)
		}
	}
	return out
}

func playerByID(players []session.Player) map[string]session.Player {
	m := make(map[string]session.Player, len(players))
	for _, p := range players {
		m[p.ID] = p
	}
	return m
}

func gamesMap(players []session.Player) map[string]int {
	m := make(map[string]int, len(players))
	for _, p := range players {
		m[p.ID] = p.GamesPlayed
	}
	return m
}

func sortedRosterIDs(players []session.Player) []string {
	ids := make([]string, len(players))
	for i := range players {
		ids[i] = players[i].ID
	}
	sort.Strings(ids)
	return ids
}

// buildPartnerIndex records who has already been on the same doubles side as whom (undirected per player).
func buildPartnerIndex(matches []session.RecordedMatch) map[string]map[string]bool {
	m := make(map[string]map[string]bool)
	link := func(a, b string) {
		if m[a] == nil {
			m[a] = make(map[string]bool)
		}
		m[a][b] = true
	}
	for _, rm := range matches {
		if len(rm.TeamAIDs) == 2 {
			link(rm.TeamAIDs[0], rm.TeamAIDs[1])
			link(rm.TeamAIDs[1], rm.TeamAIDs[0])
		}
		if len(rm.TeamBIDs) == 2 {
			link(rm.TeamBIDs[0], rm.TeamBIDs[1])
			link(rm.TeamBIDs[1], rm.TeamBIDs[0])
		}
	}
	return m
}

// repeatPartnershipBlocked is true when p1 and p2 were partners before while p1 has not yet partnered
// every other player in the eligible pool (so repeating that partner would skip the “rotate through everyone” rule).
func repeatPartnershipBlocked(p1, p2 string, idx map[string]map[string]bool, eligible []string) bool {
	if idx[p1] == nil || !idx[p1][p2] {
		return false
	}
	for _, e := range eligible {
		if e == p1 || e == p2 {
			continue
		}
		if !idx[p1][e] {
			return true
		}
	}
	return false
}

func lineupPassesPartnerRotation(lu Lineup, idx map[string]map[string]bool, eligible []string) bool {
	pairOk := func(x, y string) bool {
		return !repeatPartnershipBlocked(x, y, idx, eligible) && !repeatPartnershipBlocked(y, x, idx, eligible)
	}
	if !pairOk(lu.TeamA[0], lu.TeamA[1]) || !pairOk(lu.TeamB[0], lu.TeamB[1]) {
		return false
	}
	return true
}

func filterByPartnerRotation(cands []Lineup, matches []session.RecordedMatch, eligible []string) []Lineup {
	idx := buildPartnerIndex(matches)
	var out []Lineup
	for _, lu := range cands {
		if lineupPassesPartnerRotation(lu, idx, eligible) {
			out = append(out, lu)
		}
	}
	return out
}

// PickSuggestion chooses a doubles lineup:
// - americano: minimize sum(games_played among the four) — fairness focus.
// - mexicano: first minimize sum(games_played) to favor underplayed players, then maximize GP difference between teams.
// Partner rotation (hard preference): p1 may not partner p2 again until p1 has partnered every other eligible player
// at least once (derived from matches). If that leaves no candidates, falls back to fairness-only among remaining candidates.
// excludeKey excludes one canonical matchup (for reshuffle). excludePlayerIDs removes players from eligibility.
func PickSuggestion(players []session.Player, matches []session.RecordedMatch, style session.ShufflingStyle, excludeKey string, excludePlayerIDs []string) (*session.SuggestedMatch, string, error) {
	if style == "" {
		style = session.ShufflingStyleAmericano
	}
	exSet := make(map[string]bool)
	for _, id := range excludePlayerIDs {
		exSet[id] = true
	}
	activePool := activeShufflePlayers(players)
	eligible := filterPlayers(activePool, exSet)
	if len(eligible) < 4 {
		return nil, "", fmt.Errorf("%w: need at least 4 eligible players", ErrNoLineup)
	}
	ids := make([]string, len(eligible))
	for i := range eligible {
		ids[i] = eligible[i].ID
	}
	sort.Strings(ids)

	games := gamesMap(players)
	byID := playerByID(players)

	var candidates []Lineup
	eachQuartet(ids, func(q [4]string) {
		for _, lu := range lineupsForQuartet(q) {
			key := LineupKey(lu.TeamA, lu.TeamB)
			if excludeKey != "" && key == excludeKey {
				continue
			}
			candidates = append(candidates, lu)
		}
	})

	if len(candidates) == 0 {
		return nil, "", ErrNoLineup
	}

	if style == session.ShufflingStyleMexicano {
		return pickMexicanoLineup(eligible, matches, byID)
	}

	if rotated := filterByPartnerRotation(candidates, matches, ids); len(rotated) > 0 {
		candidates = rotated
	}

	var bestScore int
	if style == session.ShufflingStyleMexicanoTopVsTop {
		bestScore = mexicanoTopVsTopScore(games, candidates[0])
	} else if style == session.ShufflingStyleMexicanoTopVsBottom {
		bestScore = mexicanoTopVsBottomScore(games, candidates[0])
	} else {
		bestScore = fairnessScore(games, candidates[0])
	}
	for _, lu := range candidates[1:] {
		var s int
		if style == session.ShufflingStyleMexicanoTopVsTop {
			s = mexicanoTopVsTopScore(games, lu)
		} else if style == session.ShufflingStyleMexicanoTopVsBottom {
			s = mexicanoTopVsBottomScore(games, lu)
		} else {
			s = fairnessScore(games, lu)
		}
		if s < bestScore {
			bestScore = s
		}
	}

	var best []Lineup
	for _, lu := range candidates {
		var s int
		if style == session.ShufflingStyleMexicanoTopVsTop {
			s = mexicanoTopVsTopScore(games, lu)
		} else if style == session.ShufflingStyleMexicanoTopVsBottom {
			s = mexicanoTopVsBottomScore(games, lu)
		} else {
			s = fairnessScore(games, lu)
		}
		if s == bestScore {
			best = append(best, lu)
		}
	}

	chosen := best[pickIndex(len(best))]
	key := LineupKey(chosen.TeamA, chosen.TeamB)

	buildTeam := func(pair [2]string) []session.Player {
		p0 := byID[pair[0]]
		p1 := byID[pair[1]]
		return []session.Player{p0, p1}
	}

	sug := &session.SuggestedMatch{
		TeamA: buildTeam(chosen.TeamA),
		TeamB: buildTeam(chosen.TeamB),
	}
	return sug, key, nil
}

func pickIndex(n int) int {
	if n <= 1 {
		return 0
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int(binary.BigEndian.Uint64(b[:]) % uint64(n))
}
