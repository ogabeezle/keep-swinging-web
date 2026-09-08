package scheduler

import (
	"sort"

	"keep-swinging-web/internal/session"
)

// ErrNoSinglesLineup is returned when fewer than two eligible players exist.
var ErrNoSinglesLineup = ErrNoLineup

// SinglesLineup is one player per team (1 vs 1).
type SinglesLineup struct {
	PlayerA string
	PlayerB string
}

// SinglesLineupKey returns a canonical key for comparing singles matchups.
func SinglesLineupKey(a, b string) string {
	if a < b {
		return a + "|" + b
	}
	return b + "|" + a
}

// singlesFairnessScore is the sum of games_played for the two players — lower means they are more "due".
func singlesFairnessScore(games map[string]int, lu SinglesLineup) int {
	return games[lu.PlayerA] + games[lu.PlayerB]
}

// singlesMexicanoTopVsTopScore scores lineups for mexicano top-vs-top style in singles:
// 1. Prefer the most-experienced players
// 2. Among those, prefer the matchup with the highest GP difference (strongest vs weakest)
func singlesMexicanoTopVsTopScore(games map[string]int, lu SinglesLineup) int {
	sum := games[lu.PlayerA] + games[lu.PlayerB]
	diff := games[lu.PlayerA] - games[lu.PlayerB]
	if diff < 0 {
		diff = -diff
	}
	return -sum*1000 + diff
}

// singlesMexicanoTopVsBottomScore scores lineups for mexicano top-vs-bottom style in singles:
// 1. Prefer the least-experienced players (fairness)
// 2. Among those, prefer the matchup with the highest GP difference (strongest vs weakest)
func singlesMexicanoTopVsBottomScore(games map[string]int, lu SinglesLineup) int {
	sum := games[lu.PlayerA] + games[lu.PlayerB]
	diff := games[lu.PlayerA] - games[lu.PlayerB]
	if diff < 0 {
		diff = -diff
	}
	return sum*1000 - diff
}

// pickMexicanoLineupSingles implements fairness-first mexicano pairing for singles.
func pickMexicanoLineupSingles(eligible []session.Player, matches []session.RecordedMatch, byID map[string]session.Player) (*session.SuggestedMatch, string, error) {
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

	if len(fairGroup) < 2 {
		nextGP := fairGroup[len(fairGroup)-1].GamesPlayed + 1
		for _, p := range eligible {
			if p.GamesPlayed > minGP && p.GamesPlayed <= nextGP {
				fairGroup = append(fairGroup, p)
				if len(fairGroup) >= 2 {
					break
				}
			}
		}
	}

	if len(fairGroup) < 2 {
		return nil, "", ErrNoSinglesLineup
	}

	if len(matches) == 0 {
		selected := fairGroup[:2]
		key := SinglesLineupKey(selected[0].ID, selected[1].ID)
		return &session.SuggestedMatch{
			TeamA: []session.Player{selected[0]},
			TeamB: []session.Player{selected[1]},
		}, key, nil
	}

	pts := calculateMexicanoPoints(matches)
	sort.SliceStable(fairGroup, func(i, j int) bool {
		return pts[fairGroup[i].ID] > pts[fairGroup[j].ID]
	})

	selected := fairGroup[:2]
	key := SinglesLineupKey(selected[0].ID, selected[1].ID)

	return &session.SuggestedMatch{
		TeamA: []session.Player{selected[0]},
		TeamB: []session.Player{selected[1]},
	}, key, nil
}

// PickSuggestionSingles chooses a singles matchup (1 vs 1):
// - americano: minimize sum(games_played) — fairness focus.
// - mexicano: fairness-first, then leaderboard-based pairing after round 1.
func PickSuggestionSingles(players []session.Player, matches []session.RecordedMatch, style session.ShufflingStyle, excludeKey string, excludePlayerIDs []string) (*session.SuggestedMatch, string, error) {
	if style == "" {
		style = session.ShufflingStyleAmericano
	}
	exSet := make(map[string]bool)
	for _, id := range excludePlayerIDs {
		exSet[id] = true
	}
	activePool := activeShufflePlayers(players)
	eligible := filterPlayers(activePool, exSet)
	if len(eligible) < 2 {
		return nil, "", ErrNoSinglesLineup
	}
	ids := make([]string, len(eligible))
	for i := range eligible {
		ids[i] = eligible[i].ID
	}

	games := gamesMap(players)
	byID := playerByID(players)

	var candidates []SinglesLineup
	for i := 0; i < len(eligible); i++ {
		for j := i + 1; j < len(eligible); j++ {
			a, b := ids[i], ids[j]
			key := SinglesLineupKey(a, b)
			if excludeKey != "" && key == excludeKey {
				continue
			}
			candidates = append(candidates, SinglesLineup{PlayerA: a, PlayerB: b})
		}
	}

	if len(candidates) == 0 {
		return nil, "", ErrNoSinglesLineup
	}

	if style == session.ShufflingStyleMexicano {
		return pickMexicanoLineupSingles(eligible, matches, byID)
	}

	var bestScore int
	if style == session.ShufflingStyleMexicanoTopVsTop {
		bestScore = singlesMexicanoTopVsTopScore(games, candidates[0])
	} else if style == session.ShufflingStyleMexicanoTopVsBottom {
		bestScore = singlesMexicanoTopVsBottomScore(games, candidates[0])
	} else {
		bestScore = singlesFairnessScore(games, candidates[0])
	}
	for _, lu := range candidates[1:] {
		var s int
		if style == session.ShufflingStyleMexicanoTopVsTop {
			s = singlesMexicanoTopVsTopScore(games, lu)
		} else if style == session.ShufflingStyleMexicanoTopVsBottom {
			s = singlesMexicanoTopVsBottomScore(games, lu)
		} else {
			s = singlesFairnessScore(games, lu)
		}
		if s < bestScore {
			bestScore = s
		}
	}

	var best []SinglesLineup
	for _, lu := range candidates {
		var s int
		if style == session.ShufflingStyleMexicanoTopVsTop {
			s = singlesMexicanoTopVsTopScore(games, lu)
		} else if style == session.ShufflingStyleMexicanoTopVsBottom {
			s = singlesMexicanoTopVsBottomScore(games, lu)
		} else {
			s = singlesFairnessScore(games, lu)
		}
		if s == bestScore {
			best = append(best, lu)
		}
	}

	chosen := best[pickIndex(len(best))]
	key := SinglesLineupKey(chosen.PlayerA, chosen.PlayerB)

	sug := &session.SuggestedMatch{
		TeamA: []session.Player{byID[chosen.PlayerA]},
		TeamB: []session.Player{byID[chosen.PlayerB]},
	}
	return sug, key, nil
}
