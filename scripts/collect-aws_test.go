package main

import (
	"strings"
	"testing"
)

func writeGroups(messages ...string) string {
	mg := newMessageGroups()
	for _, m := range messages {
		mg.Add(m)
	}
	var out strings.Builder
	mg.Write(&out)
	return out.String()
}

func TestMessageGroupsSingleMessageUnchanged(t *testing.T) {
	msg := "Request limit exceeded. Account 123456789012 has been throttled on ec2:RunInstances because it exceeded its request rate limit."
	got := writeGroups(msg)
	want := "[    1] " + msg + "\n"
	if got != want {
		t.Errorf("got\n%v\nwant\n%v", got, want)
	}
}

func TestMessageGroupsNumbers(t *testing.T) {
	got := writeGroups(
		"Your Spot request price of 0.01736 is lower than the minimum required Spot request fulfillment price of 0.0182.",
		"Your Spot request price of 0.112 is lower than the minimum required Spot request fulfillment price of 0.12.",
		"Your Spot request price of 0.05264 is lower than the minimum required Spot request fulfillment price of 0.0703.",
	)
	want := "[    3] Your Spot request price of <A> is lower than the minimum required Spot request fulfillment price of <B>.\n" +
		"        <A>: 0.01736 to 0.112\n" +
		"        <B>: 0.0182 to 0.12\n"
	if got != want {
		t.Errorf("got\n%v\nwant\n%v", got, want)
	}
}

func TestMessageGroupsListsAndRepeatedDetails(t *testing.T) {
	got := writeGroups(
		"c6in.xlarge @ eu-west-3c: We currently do not have sufficient c6in.xlarge capacity in the Availability Zone you requested (eu-west-3c). You can currently get c6in.xlarge capacity by choosing eu-west-3a, eu-west-3b.",
		"c6in.xlarge @ us-east-1a: We currently do not have sufficient c6in.xlarge capacity in the Availability Zone you requested (us-east-1a). You can currently get c6in.xlarge capacity by choosing us-east-1b, us-east-1c, us-east-1d.",
		"r7i.large @ eu-west-3c: We currently do not have sufficient r7i.large capacity in the Availability Zone you requested (eu-west-3c). You can currently get r7i.large capacity by choosing eu-west-3a, eu-west-3b.",
		"Max spot instance count exceeded",
	)
	want := "[    3] <A> @ <B>: We currently do not have sufficient <A> capacity in the Availability Zone you requested (<B>). You can currently get <A> capacity by choosing <C>.\n" +
		"        <A>: c6in.xlarge (2) | r7i.large\n" +
		"        <B>: eu-west-3c (2) | us-east-1a\n" +
		"        <C>: eu-west-3a, eu-west-3b (2) | us-east-1b, us-east-1c, us-east-1d\n" +
		"[    1] Max spot instance count exceeded\n"
	if got != want {
		t.Errorf("got\n%v\nwant\n%v", got, want)
	}
}

func TestMessageGroupsUniqueValues(t *testing.T) {
	var messages []string
	for _, id := range []string{"08d917e5-2b27", "247c88f7-3d35", "266ba8dc-b9ee", "27865292-4a32", "297f7888-14f0", "2a7f7888-14f0"} {
		messages = append(messages, "api error, RequestID: "+id+", RequestExpired")
	}
	got := writeGroups(messages...)
	want := "[    6] api error, RequestID: <A>, RequestExpired\n" +
		"        <A>: 6 different values, like 08d917e5-2b27\n"
	if got != want {
		t.Errorf("got\n%v\nwant\n%v", got, want)
	}
}
