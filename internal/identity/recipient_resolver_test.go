package identity

import (
	"testing"

	"github.com/Dawn-kim-official/blueclaw/internal/policy"
)

func testPeople() []policy.PersonPolicy {
	return []policy.PersonPolicy{
		{PersonID: "person-rain", DisplayName: "김테스트", Emails: []string{"rain@example.com"}},
		{PersonID: "person-lee", DisplayName: "이샘플", Emails: []string{"lee@example.com"}},
		{PersonID: "person-chanhee", DisplayName: "최견본", Emails: []string{"gyeonbon@example.com"}},
	}
}

func testAccounts() []PlatformAccountIdentity {
	return []PlatformAccountIdentity{
		{Platform: "mattermost", ExternalUserID: "mm-rain", Email: "rain@example.com", DisplayName: "rain", PersonID: "person-rain"},
		{Platform: "mattermost", ExternalUserID: "mm-lee", Email: "lee@example.com", DisplayName: "lee", PersonID: "person-lee"},
		{Platform: "slack", ExternalUserID: "slack-rain", Email: "rain@example.com", DisplayName: "rain", PersonID: "person-rain"},
	}
}

func TestResolveRecipientByFullDisplayName(test *testing.T) {
	resolution := ResolveRecipient("mattermost", "김테스트", testPeople(), testAccounts())
	if resolution.Status != RecipientResolved {
		test.Fatalf("expected resolved, got %+v", resolution)
	}
	if resolution.Recipient.ExternalUserID != "mm-rain" {
		test.Fatalf("expected mattermost account, got %+v", resolution.Recipient)
	}
	if resolution.Recipient.Username != "rain" {
		test.Fatalf("expected mattermost username, got %+v", resolution.Recipient)
	}
}

func TestResolveRecipientDoesNotMatchContainedDisplayName(test *testing.T) {
	people := append(testPeople(), policy.PersonPolicy{PersonID: "person-rain-two", DisplayName: "김테스트수", Emails: []string{"rain2@example.com"}})
	resolution := ResolveRecipient("mattermost", "테스트", people, testAccounts())
	if resolution.Status != RecipientNotFound {
		test.Fatalf("expected not_found, got %+v", resolution)
	}
}

func TestResolveRecipientByLearnedAccountAlias(test *testing.T) {
	resolution := ResolveRecipient("mattermost", "@rain", testPeople(), testAccounts())
	if resolution.Status != RecipientResolved || resolution.Recipient.PersonID != "person-rain" {
		test.Fatalf("expected resolution via account alias, got %+v", resolution)
	}
}

func TestResolveRecipientByExactPersonIDAndEmail(test *testing.T) {
	for _, hint := range []string{"person-rain", "RAIN@DAWN.KIM"} {
		resolution := ResolveRecipient("mattermost", hint, testPeople(), testAccounts())
		if resolution.Status != RecipientResolved || resolution.Recipient.PersonID != "person-rain" {
			test.Fatalf("hint %q resolution = %+v", hint, resolution)
		}
	}
}

func TestResolveRecipientUnlinkedPersonKeepsEmails(test *testing.T) {
	resolution := ResolveRecipient("mattermost", "최견본", testPeople(), testAccounts())
	if resolution.Status != RecipientUnlinked {
		test.Fatalf("expected unlinked, got %+v", resolution)
	}
	if len(resolution.Recipient.Emails) != 1 || resolution.Recipient.Emails[0] != "gyeonbon@example.com" {
		test.Fatalf("expected fallback emails, got %+v", resolution.Recipient)
	}
}

func TestResolveRecipientAmbiguousReturnsCandidates(test *testing.T) {
	people := []policy.PersonPolicy{
		{PersonID: "person-one", DisplayName: "김민수"},
		{PersonID: "person-two", DisplayName: "김민수"},
	}
	resolution := ResolveRecipient("mattermost", "김민수", people, nil)
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
		{Platform: "slack", ExternalUserID: "slack-rain", Email: "rain@example.com", DisplayName: "rain", PersonID: "person-rain"},
	}
	resolution := ResolveRecipient("mattermost", "김테스트", testPeople(), accounts)
	if resolution.Status != RecipientUnlinked {
		test.Fatalf("expected unlinked without mattermost account, got %+v", resolution)
	}
}

func TestResolveRecipientLinksAccountByEmailWhenPersonIDMissing(test *testing.T) {
	accounts := []PlatformAccountIdentity{
		{Platform: "mattermost", ExternalUserID: "mm-rain", Email: "rain@example.com", DisplayName: "rain"},
	}
	resolution := ResolveRecipient("mattermost", "김테스트", testPeople(), accounts)
	if resolution.Status != RecipientResolved || resolution.Recipient.ExternalUserID != "mm-rain" {
		test.Fatalf("expected email-linked resolution, got %+v", resolution)
	}
}

func TestResolveRecipientTrustsProfileEmailOverLearnedPersonID(test *testing.T) {
	accounts := []PlatformAccountIdentity{
		{Platform: "mattermost", ExternalUserID: "mm-rain", Email: "rain@example.com", DisplayName: "rain", PersonID: "person-lee"},
	}
	resolution := ResolveRecipient("mattermost", "김테스트", testPeople(), accounts)
	if resolution.Status != RecipientResolved || resolution.Recipient.PersonID != "person-rain" || resolution.Recipient.ExternalUserID != "mm-rain" {
		test.Fatalf("expected profile email attribution to win, got %+v", resolution)
	}
	leeResolution := ResolveRecipient("mattermost", "rain", testPeople(), accounts)
	if leeResolution.Status != RecipientResolved || leeResolution.Recipient.PersonID != "person-rain" {
		test.Fatalf("expected crossed row not to leak into other people, got %+v", leeResolution)
	}
}

func TestResolveRecipientRejectsPartialAndSuffixedNames(test *testing.T) {
	people := []policy.PersonPolicy{{PersonID: "person-1", DisplayName: "김테스트", Emails: []string{"rain@example.com"}}}
	accounts := []PlatformAccountIdentity{{Platform: "mattermost", PersonID: "person-1", Email: "rain@example.com", ExternalUserID: "mm-1"}}
	for _, hint := range []string{"테스트", "테스트이", "테스트님", "테스트씨", "김테스트님", "김테스트이형", "이"} {
		resolution := ResolveRecipient("mattermost", hint, people, accounts)
		if resolution.Status != RecipientNotFound {
			test.Fatalf("hint %q resolution = %+v", hint, resolution)
		}
	}
}
