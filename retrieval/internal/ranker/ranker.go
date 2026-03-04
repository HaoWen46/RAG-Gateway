package ranker

// Ranker scores and ranks retrieved sections.
type Ranker struct{}

func New() *Ranker {
	return &Ranker{}
}

// Rank sorts sections by relevance score.
// TODO: Implement ranking logic with trust-tier boosting.
func (r *Ranker) Rank(sections []Section) []Section {
	return sections
}

type Section struct {
	ID    string
	Score float64
}
