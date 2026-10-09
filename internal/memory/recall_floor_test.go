package memory

import "testing"

// Recall returned up to limit memories whatever the query: `recall "zzqx"`
// listed every memory in the project, and a context pack for a Stripe webhook
// packed the staging port and the commit style under "What you've told me"
// (#44). With no vector to go on, a memory that shares no word with the query
// has no claim to be relevant to it.
func TestARecallThatMatchesNothingReturnsNothing(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "staging runs on port 8443", Fact, 0.5, nil)
	storeVec(t, db, "we use pnpm, never npm or yarn", Preference, 0.5, nil)

	got, err := RecallInProject(db, nil, "", "zzqx", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a query nothing matches returned %v", texts(got))
	}
}

// Every returned memory had uses+1, relevant or not, and uses raises salience,
// which raises rank next time — so whatever came back early kept coming back.
// A memory the vector arm puts nowhere near the query is not returned, and so
// not reinforced.
func TestARecallReinforcesOnlyWhatWasRelevant(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "the auth service rotates its signing keys weekly", Fact, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "tabs over spaces", Preference, 0.5, []float32{0, 1, 0})

	got, err := Recall(db, embedRuntime(t, false), "nomic-embed-text", "key rotation", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "the auth service rotates its signing keys weekly" {
		t.Errorf("want only the auth memory, got %v", texts(got))
	}
	var uses int
	db.QueryRow(`SELECT uses FROM memories WHERE text = 'tabs over spaces'`).Scan(&uses)
	if uses != 0 {
		t.Errorf("an unrelated memory was reinforced to uses=%d by a recall it did not answer", uses)
	}
}

// nomic-embed-text puts unrelated short sentences around 0.36-0.48 and a clear
// answer at 0.7-0.87, so an absolute floor alone lets a whole band of noise
// through behind a strong hit. Something far below the best match is not an
// answer to this query, even if it clears the floor.
func TestAMemoryFarBelowTheBestMatchIsLeftOut(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "billing is owned by the payments team", Fact, 0.5, []float32{1, 0, 0})
	storeVec(t, db, "billing invoices go out on the first", Fact, 0.5, []float32{0.9, 0.436, 0})
	storeVec(t, db, "staging runs on port 8443", Fact, 0.5, []float32{0.6, 0.8, 0})

	got, err := Recall(db, embedRuntime(t, false), "nomic-embed-text", "who owns it", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || containsText(got, "staging") {
		t.Errorf("want the two billing memories and not the staging one, got %v", texts(got))
	}
}

// Without an embedding model the words are the only evidence, and the floor
// held them to exact matches: "how should I reply" no longer found "the user
// prefers short replies", which it had reached before only because recall
// returned everything.
func TestWithoutEmbeddingsAnInflectedWordStillCounts(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "the user prefers short replies", Preference, 0.5, nil)
	storeVec(t, db, "the cat sat on the mat", Fact, 0.5, nil)

	got, err := RecallInProject(db, nil, "", "how should I reply to the cache question", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !containsText(got, "short replies") {
		t.Errorf("want only the replies memory, got %v", texts(got))
	}
}

// The gate counted any shared word, and "we" is not a stopword to bm25, so
// "what database do we use" returned "we ship releases with argo cd" and
// reinforced it (#224). A pronoun or a modal ties a question to nothing.
func TestAPronounTheQueryAndAMemoryShareIsNotEvidence(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "we ship releases with argo cd", Fact, 0.5, nil)
	storeVec(t, db, "our team should review every migration", Fact, 0.5, nil)

	got, err := RecallInProject(db, nil, "", "what database should we use for our app", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("memories tied to the query only by we/our/should came back: %v", texts(got))
	}
}

// sameStem accepted any three shared letters when the shorter word had at most
// five, so "use" matched "user" and "replies" matched "repo" (#224). A shared
// prefix is an inflection only when what follows it on each side is one.
func TestAWordThatOnlySharesItsFirstLettersIsNotTheSameWord(t *testing.T) {
	db := testDB(t)
	storeVec(t, db, "the user prefers short replies", Preference, 0.5, nil)
	storeVec(t, db, "credentials for the repo come from the secrets manager", Fact, 0.5, nil)

	got, err := RecallInProject(db, nil, "", "which database do we use", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("use~user leaked: %v", texts(got))
	}

	got, err = RecallInProject(db, nil, "", "how should I format my replies", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !containsText(got, "short replies") {
		t.Errorf("want only the replies memory, not replies~repo, got %v", texts(got))
	}
}

func TestSameStemKeepsInflectionsAndRefusesSharedPrefixes(t *testing.T) {
	for _, p := range [][2]string{
		{"reply", "replies"}, {"own", "owns"}, {"own", "owned"}, {"owns", "owned"},
		{"release", "releases"}, {"release", "released"}, {"release", "releasing"},
		{"replies", "replied"}, {"stop", "stopped"}, {"stop", "stopping"}, {"deploy", "deploys"},
	} {
		if !sameStem(p[0], p[1]) || !sameStem(p[1], p[0]) {
			t.Errorf("%s and %s are one word inflected and were not matched", p[0], p[1])
		}
	}
	for _, p := range [][2]string{
		{"use", "user"}, {"replies", "repo"}, {"cat", "cache"}, {"car", "care"}, {"form", "format"},
	} {
		if sameStem(p[0], p[1]) || sameStem(p[1], p[0]) {
			t.Errorf("%s and %s share letters, not a word, and were matched", p[0], p[1])
		}
	}
}
