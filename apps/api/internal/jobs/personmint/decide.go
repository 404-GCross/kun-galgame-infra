package personmint

import (
	"fmt"
	"regexp"
	"sort"
)

var orgNamePattern = regexp.MustCompile(`(?i)(株式会社|有限会社|合同会社|合資会社|合名会社|サークル|スタジオ|制作委員会|工房|` +
	`studio|\binc\.?$|\bltd\.?$|\bllc\b|\bcorp(oration)?\b|co\.,? ?ltd|\bcompany\b|\bproject\b|\bteam\b|\bgroup\b|\bsoftware\b)`)

type mintPlan struct {
	ClusterID      string
	Members        []int64
	Names          []string
	HostID         int64
	Host           *personState
	PrimaryID      int64
	DisplayName    string
	Gender         *int16
	GenderFrom     string
	BirthY         *int16
	BirthM         *int16
	BirthD         *int16
	LinkFill       []int64
	LinksAlready   int
	Anchors        []anchorKey
	AnchorsNew     []anchorKey
	AnchorsAlready int
	Conflict       *GenderConflict
	BirthConflict  bool
}

type decider struct {
	env          *environment
	split        map[int64]bool
	crossCluster map[int64]int
}

func (d *decider) decide(c Cluster) (*mintPlan, *Defer) {
	names := make([]string, 0, len(c.CreditNameIDs))
	for _, id := range c.CreditNameIDs {
		names = append(names, d.env.members[id].Name)
	}
	reasons := map[DeferReason]string{}

	for _, id := range c.CreditNameIDs {
		if d.split[id] {
			reasons[DeferE4Split] = fmt.Sprintf("credit_name %d on the E4 split worklist", id)
			break
		}
	}
	for _, id := range c.CreditNameIDs {
		if d.env.labelNorms[d.env.members[id].NameNorm] {
			reasons[DeferOrgLabel] = fmt.Sprintf("credit_name %d (%s) collides with a label name", id, d.env.members[id].Name)
			break
		}
	}
	for _, id := range c.CreditNameIDs {
		if orgNamePattern.MatchString(d.env.members[id].Name) {
			reasons[DeferOrgPattern] = fmt.Sprintf("credit_name %d (%s) carries an organization marker", id, d.env.members[id].Name)
			break
		}
	}

	anchors := d.anchorSet(c)
	hosts := d.hostCandidates(c, anchors)
	if len(hosts) > 1 {
		reasons[DeferPersonMulti] = fmt.Sprintf("persons %v", hosts)
	}
	for _, id := range c.CreditNameIDs {
		if pid, ok := d.env.personOfMember[id]; ok {
			if n := d.crossCluster[pid]; n > 1 {
				reasons[DeferPersonCrossCluster] = fmt.Sprintf("person %d spans %d auto clusters", pid, n)
				break
			}
		}
	}

	if len(reasons) > 0 {
		dfr := &Defer{ClusterID: c.ClusterID, Members: c.CreditNameIDs, Names: names}
		for _, r := range deferOrder {
			if detail, hit := reasons[r]; hit {
				if dfr.Reason == "" {
					dfr.Reason, dfr.Detail = r, detail
					continue
				}
				dfr.Also = append(dfr.Also, r)
			}
		}
		return nil, dfr
	}

	plan := &mintPlan{ClusterID: c.ClusterID, Members: c.CreditNameIDs, Names: names, Anchors: anchors}
	if len(hosts) == 1 {
		plan.HostID = hosts[0]
		plan.Host = d.env.persons[plan.HostID]
	}
	plan.PrimaryID = d.primaryMember(c)
	plan.DisplayName = d.env.members[plan.PrimaryID].Name
	for _, id := range c.CreditNameIDs {
		pid, linked := d.env.personOfMember[id]
		switch {
		case !linked:
			plan.LinkFill = append(plan.LinkFill, id)
		case pid == plan.HostID:
			plan.LinksAlready++
		}
	}
	for _, a := range anchors {
		if _, exists := d.env.et0Owner[a]; exists {
			plan.AnchorsAlready++
			continue
		}
		plan.AnchorsNew = append(plan.AnchorsNew, a)
	}
	d.survivorship(c, plan)
	return plan, nil
}

// anchorSet builds the cluster's et=0 (person-grained) anchors out of its
// members' et=1 anchors. The vndb lane is the one that changes id space: the
// credit name is anchored on the ALIAS (staff_alias.aid), and the person is
// anchored on the STAFF id it belongs to — using the aid here would file an
// alias id in the person id space and collide with nothing, silently
// (wave 152 §2.1, §7.6). Every other source is identity-grained already.
func (d *decider) anchorSet(c Cluster) []anchorKey {
	seen := map[anchorKey]bool{}
	var out []anchorKey
	add := func(a anchorKey) {
		if seen[a] {
			return
		}
		seen[a] = true
		out = append(out, a)
	}
	for _, id := range c.CreditNameIDs {
		for _, a := range d.env.anchors[id] {
			switch a.SourceID {
			case d.env.srcVNDB:
				aid, err := atoi64(a.ExternalID)
				if err != nil {
					continue
				}
				if staffID, ok := d.env.staffOfAid[aid]; ok {
					add(anchorKey{SourceID: d.env.srcVNDB, ExternalID: staffID})
				}
			case d.env.srcBangumi, d.env.srcDLsite, d.env.srcEG:
				add(a)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceID != out[j].SourceID {
			return out[i].SourceID < out[j].SourceID
		}
		return out[i].ExternalID < out[j].ExternalID
	})
	return out
}

func (d *decider) hostCandidates(c Cluster, anchors []anchorKey) []int64 {
	set := map[int64]bool{}
	for _, id := range c.CreditNameIDs {
		if pid, ok := d.env.personOfMember[id]; ok {
			set[pid] = true
		}
	}
	for _, a := range anchors {
		if pid, ok := d.env.et0Owner[a]; ok {
			set[pid] = true
		}
	}
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (d *decider) primaryMember(c Cluster) int64 {
	var main []int64
	for _, id := range c.CreditNameIDs {
		for _, a := range d.env.anchors[id] {
			if a.SourceID != d.env.srcVNDB {
				continue
			}
			aid, err := atoi64(a.ExternalID)
			if err != nil {
				continue
			}
			if staffID, ok := d.env.staffOfAid[aid]; ok && d.env.staffMain[staffID] == aid {
				main = append(main, id)
			}
		}
	}
	if len(main) > 0 {
		sort.Slice(main, func(i, j int) bool { return main[i] < main[j] })
		return main[0]
	}
	best := c.CreditNameIDs[0]
	for _, id := range c.CreditNameIDs[1:] {
		if d.env.creditCount[id] > d.env.creditCount[best] {
			best = id
		}
	}
	return best
}

func (d *decider) survivorship(c Cluster, plan *mintPlan) {
	genders := map[int16]bool{}
	var raw []string
	fromVNDB := false
	for _, a := range plan.Anchors {
		switch a.SourceID {
		case d.env.srcVNDB:
			if g, ok := normGender(d.env.staffGender[a.ExternalID]); ok {
				genders[g] = true
				raw = append(raw, "vndb:"+d.env.staffGender[a.ExternalID])
				fromVNDB = true
			}
		case d.env.srcBangumi:
			id, err := atoi64(a.ExternalID)
			if err != nil {
				continue
			}
			f := d.env.bgmFacts[id]
			if g, ok := normGender(f.Gender); ok {
				genders[g] = true
				raw = append(raw, "bangumi:"+f.Gender)
			}
		}
	}
	switch len(genders) {
	case 0:
	case 1:
		for g := range genders {
			v := g
			plan.Gender = &v
		}
		plan.GenderFrom = sourceBangumi
		if fromVNDB {
			plan.GenderFrom = sourceVNDB
		}
	default:
		plan.Conflict = &GenderConflict{ClusterID: c.ClusterID, Values: raw, Names: plan.Names}
	}

	var dates []birthDate
	for _, a := range plan.Anchors {
		if a.SourceID != d.env.srcBangumi {
			continue
		}
		id, err := atoi64(a.ExternalID)
		if err != nil {
			continue
		}
		f := d.env.bgmFacts[id]
		if f.BirthY == nil && f.BirthM == nil && f.BirthD == nil {
			continue
		}
		dates = append(dates, birthDate{f.BirthY, f.BirthM, f.BirthD})
	}
	for i, dt := range dates {
		if i > 0 && !sameDate(dates[0], dt) {
			plan.BirthConflict = true
			return
		}
	}
	if len(dates) > 0 {
		plan.BirthY, plan.BirthM, plan.BirthD = dates[0].y, dates[0].m, dates[0].d
	}
}

type birthDate struct{ y, m, d *int16 }

func sameDate(a, b birthDate) bool {
	return eq16(a.y, b.y) && eq16(a.m, b.m) && eq16(a.d, b.d)
}

func eq16(a, b *int16) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func atoi64(s string) (int64, error) {
	var n int64
	if s == "" {
		return 0, fmt.Errorf("empty id")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("non-numeric id %q", s)
		}
		n = n*10 + int64(r-'0')
	}
	return n, nil
}
