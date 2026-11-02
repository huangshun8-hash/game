package validx

import "testing"

// --- ValidateUUID 测试 ---

func TestValidateUUID_Valid(t *testing.T) {
	cases := []string{
		"abc123",
		"user_001",
		"device-id-123",
		"a",
		"abc-def_ghi-123",
		"1234567890abcdef",
	}
	for _, c := range cases {
		if err := ValidateUUID(c); err != nil {
			t.Errorf("expected valid for %q, got error: %v", c, err)
		}
	}
}

func TestValidateUUID_Empty(t *testing.T) {
	err := ValidateUUID("")
	if err == nil {
		t.Fatal("expected error for empty uuid")
	}
	if err.Error() != "uuid is required" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateUUID_TooLong(t *testing.T) {
	long := make([]byte, uuidMaxLen+1)
	for i := range long {
		long[i] = 'a'
	}
	err := ValidateUUID(string(long))
	if err == nil {
		t.Fatal("expected error for too-long uuid")
	}
	if err.Error() != "uuid exceeds maximum length" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateUUID_MaxLength(t *testing.T) {
	exact := make([]byte, uuidMaxLen)
	for i := range exact {
		exact[i] = 'a'
	}
	if err := ValidateUUID(string(exact)); err != nil {
		t.Errorf("expected valid for max-length uuid, got error: %v", err)
	}
}

func TestValidateUUID_UppercaseAllowed(t *testing.T) {
	cases := []string{
		"ABC",
		"Abc123",
		"USER_001",
		"Device-Id",
	}
	for _, c := range cases {
		if err := ValidateUUID(c); err != nil {
			t.Errorf("expected valid for uppercase uuid %q, got error: %v", c, err)
		}
	}
}

func TestValidateUUID_InvalidChars(t *testing.T) {
	cases := []string{
		"abc 123", // 空格
		"abc@123", // @
		"abc.123", // 点号
		"abc!123", // 感叹号
		"abc/123", // 斜杠
		"中文",      // 中文
	}
	for _, c := range cases {
		if err := ValidateUUID(c); err == nil {
			t.Errorf("expected error for invalid uuid %q", c)
		}
	}
}
