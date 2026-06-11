package identity

import (
	"testing"

	"blueclaw/internal/policy"
)

func testPeople() []policy.PersonPolicy {
	return []policy.PersonPolicy{
		{PersonID: "person-rain", DisplayName: "신우경", Emails: []string{"rain@dawn.kim"}},
		{PersonID: "person-lee", DisplayName: "이동하", Emails: []string{"lee@dawn.kim"}},
		{PersonID: "person-chanhee", DisplayName: "이찬희", Emails: []string{"chanhee2468@gmail.com"}},
	}
}

func testAccounts() []PlatformAccountIdentity {
	return []PlatformAccountIdentity{
		{Platform: "mattermost", ExternalUserID: "mm-rain", Email: "rain@dawn.kim", DisplayName: "rain", PersonID: "person-rain"},
		{Platform: "mattermost", ExternalUserID: "mm-lee", Email: "lee@dawn.kim", DisplayName: "lee", PersonID: "person-lee"},
		{Platform: "slack", ExternalUserID: "slack-rain", Email: "rain@dawn.kim", DisplayName: "rain", PersonID: "person-rain"},
	}
}

func TestResolveRecipientByPartialKoreanName(test *testing.T) {
	resolution := ResolveRecipient("mattermost", "우경", testPeople(), testAccounts())
	if resolution.Status != RecipientResolved {
		test.Fatalf("expected resolved, got %+v", resolution)
	}
	if resolution.Recipient.ExternalUserID != "mm-rain" {
		test.Fatalf("expected mattermost account, got %+v", resolution.Recipient)
	}
}

func TestResolveRecipientPrefersExactMatchOverContains(test *testing.T) {
	people := append(testPeople(), policy.PersonPolicy{PersonID: "person-rain-two", DisplayName: "신우경수", Emails: []string{"rain2@dawn.kim"}})
	resolution := ResolveRecipient("mattermost", "신우경", people, testAccounts())
	if resolution.Status != RecipientResolved {
		test.Fatalf("expected resolved, got %+v", resolution)
	}
	if resolution.Recipient.PersonID != "person-rain" {
		test.Fatalf("expected exact match winner, got %+v", resolution.Recipient)
	}
}

func TestResolveRecipientByLearnedAccountAlias(test *testing.T) {
	resolution := ResolveRecipient("mattermost", "@rain", testPeople(), testAccounts())
	if resolution.Status != RecipientResolved || resolution.Recipient.PersonID != "person-rain" {
		test.Fatalf("expected resolution via account alias, got %+v", resolution)
	}
}

func TestResolveRecipientUnlinkedPersonKeepsEmails(test *testing.T) {
	resolution := ResolveRecipient("mattermost", "찬희", testPeople(), testAccounts())
	if resolution.Status != RecipientUnlinked {
		test.Fatalf("expected unlinked, got %+v", resolution)
	}
	if len(resolution.Recipient.Emails) != 1 || resolution.Recipient.Emails[0] != "chanhee2468@gmail.com" {
		test.Fatalf("expected fallback emails, got %+v", resolution.Recipient)
	}
}

func TestResolveRecipientAmbiguousReturnsCandidates(test *testing.T) {
	resolution := ResolveRecipient("mattermost", "이", testPeople(), testAccounts())
	if resolution.Status != RecipientAmbiguous {
		test.Fatalf("expected ambiguous, got %+v", resolution)
	}
	if len(resolution.Candidates) != 2 {
		test.Fatalf("expected two candidates, got %+v", resolution.Candidates)
	}
}

func TestResolveRecipientNotFoundCarriesDirectory(test *testing.T) {
	resolution := ResolveRecipient("mattermost", "없는사람", testPeople(), testAccounts())
	if resolution.Status != RecipientNotFound {
		test.Fatalf("expected not_found, got %+v", resolution)
	}
	if len(resolution.ApprovedPeople) != 3 {
		test.Fatalf("expected approved people directory, got %+v", resolution.ApprovedPeople)
	}
}

func TestResolveRecipientIgnoresOtherPlatformAccounts(test *testing.T) {
	accounts := []PlatformAccountIdentity{
		{Platform: "slack", ExternalUserID: "slack-rain", Email: "rain@dawn.kim", DisplayName: "rain", PersonID: "person-rain"},
	}
	resolution := ResolveRecipient("mattermost", "우경", testPeople(), accounts)
	if resolution.Status != RecipientUnlinked {
		test.Fatalf("expected unlinked without mattermost account, got %+v", resolution)
	}
}

func TestResolveRecipientLinksAccountByEmailWhenPersonIDMissing(test *testing.T) {
	accounts := []PlatformAccountIdentity{
		{Platform: "mattermost", ExternalUserID: "mm-rain", Email: "rain@dawn.kim", DisplayName: "rain"},
	}
	resolution := ResolveRecipient("mattermost", "우경", testPeople(), accounts)
	if resolution.Status != RecipientResolved || resolution.Recipient.ExternalUserID != "mm-rain" {
		test.Fatalf("expected email-linked resolution, got %+v", resolution)
	}
}
