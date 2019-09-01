package sticky

import (
	"testing"

	"github.com/twmb/kgo/kmsg"
)

type udBuilder struct {
	version     int
	generation  int
	assignments map[string][]int32
}

func oldUD() *udBuilder {
	return &udBuilder{
		version: 0,
	}
}

func newUD() *udBuilder {
	return &udBuilder{
		version:    1,
		generation: 1,
	}
}

func (b *udBuilder) assign(topic string, partitions ...int32) *udBuilder {
	if b.assignments == nil {
		b.assignments = make(map[string][]int32)
	}
	b.assignments[topic] = partitions
	return b
}

func (b *udBuilder) encode() []byte {
	return udEncode(b.version, b.generation, b.assignments)
}

func udEncode(version, generation int, assignments map[string][]int32) []byte {
	switch version {
	case 0:
		v0 := new(kmsg.StickyMemberMetadataV0)
		for topic, partitions := range assignments {
			v0.CurrentAssignment = append(v0.CurrentAssignment, kmsg.StickyMemberMetadataV0CurrentAssignment{
				topic,
				partitions,
			})
		}
		return v0.AppendTo(nil)
	case 1:
		v1 := &kmsg.StickyMemberMetadataV1{
			Generation: int32(generation),
		}
		for topic, partitions := range assignments {
			v1.CurrentAssignment = append(v1.CurrentAssignment, kmsg.StickyMemberMetadataV1CurrentAssignment{
				topic,
				partitions,
			})
		}
		return v1.AppendTo(nil)
	}
	return nil
}

func testEqualDivvy(t *testing.T, plan Plan, expSticky int, input []GroupMember) {
	t.Helper()

	min := 1 << 31
	max := 0
	var stickiness int
	for member, topics := range plan {
		stickiness += getStickiness(member, topics, input)

		assigned := 0
		for _, partitions := range topics {
			assigned += len(partitions)
		}
		if assigned < min {
			min = assigned
		}
		if assigned > max {
			max = assigned
		}
	}
	if max-min > 1 {
		t.Errorf("plan not equally divvied, min assigned %d; max %d", min, max)
	}

	if stickiness != expSticky {
		t.Errorf("got sticky %d != exp %d", stickiness, expSticky)
	}
}

func testStickyResult(
	t *testing.T,
	plan Plan,
	input []GroupMember,
	expSticky int,
	exp map[int]resultOptions,
) {
	t.Helper()

	var stickiness int
	for member, topics := range plan {
		stickiness += getStickiness(member, topics, input)
		nparts := len(plan[member])

		expParts := exp[nparts]
		expParts.times--
		exp[nparts] = expParts
		if expParts.times < 0 {
			t.Errorf("saw partition count %d too many times (%d extra)", nparts, -expParts.times)
		}
		var found bool
		for _, candidate := range expParts.candidates {
			if candidate == member {
				found = true
			}
		}
		if !found {
			t.Errorf("found member %s unexpectedly with %d parts", member, nparts)
		}
	}

	for nparts, expParts := range exp {
		if expParts.times > 0 {
			t.Errorf("did not see %d parts enough; %d expectations remaining", nparts, expParts.times)
		}
	}

	if stickiness != expSticky {
		t.Errorf("got sticky %d != exp %d", stickiness, expSticky)
	}
}

func getStickiness(member string, memberPlan map[string][]int32, input []GroupMember) int {
	var priorPlan []topicPartition
	for _, in := range input {
		if in.ID == member {
			priorPlan, _ = deserializeUserData(in.Version, in.UserData)
		}
	}
	if len(priorPlan) == 0 {
		return 0
	}

	var stickiness int
	for _, priorPartition := range priorPlan {
		topicAssigned := memberPlan[priorPartition.topic]
		if len(topicAssigned) == 0 {
			continue
		}
		for _, partition := range topicAssigned {
			if partition == priorPartition.partition {
				stickiness++
			}
		}
	}
	return stickiness
}

func testPlanUsage(t *testing.T, plan Plan, topics map[string][]int32, unused []string) {
	t.Helper()

	all := make(map[topicPartition]int)
	for _, skip := range unused {
		delete(topics, skip)
	}

	for topic, partitions := range topics {
		for _, partition := range partitions {
			all[topicPartition{topic, int32(partition)}] = 0
		}
	}

	for member, topics := range plan {
		for topic, partitions := range topics {
			for _, partition := range partitions {
				tp := topicPartition{topic, partition}
				times, exists := all[tp]
				if !exists {
					t.Errorf("%s contains unexpected partition %s/%d", member, topic, partition)
					continue
				}
				if times > 0 {
					t.Errorf("%s caused partition %s/%d to be multiply used (now %d times)", member, topic, partition, times)
				}
				all[tp] = times + 1
			}
		}
	}
}
