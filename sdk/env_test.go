package ncloud

import "testing"

func TestRebaseURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		env  Environment
		want string
	}{
		{
			"public identity",
			"https://vserver.apigw.ntruss.com/vserver/v2",
			EnvPublic,
			"https://vserver.apigw.ntruss.com/vserver/v2",
		},
		{
			"public → financial",
			"https://vserver.apigw.ntruss.com/vserver/v2",
			EnvFinancial,
			"https://vserver.apigw.fin-ntruss.com/vserver/v2",
		},
		{
			"public → gov",
			"https://sourcecommit.apigw.ntruss.com/api/v1",
			EnvGov,
			"https://sourcecommit.apigw.gov-ntruss.com/api/v1",
		},
		{
			"fin → public",
			"https://sts.apigw.fin-ntruss.com/api/v1",
			EnvPublic,
			"https://sts.apigw.ntruss.com/api/v1",
		},
		{
			"gov → fin",
			"https://ncloud.apigw.gov-ntruss.com/vserver/v2",
			EnvFinancial,
			"https://ncloud.apigw.fin-ntruss.com/vserver/v2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RebaseURL(tc.in, tc.env); got != tc.want {
				t.Errorf("RebaseURL(%q, %q) = %q, want %q", tc.in, tc.env, got, tc.want)
			}
		})
	}
}
