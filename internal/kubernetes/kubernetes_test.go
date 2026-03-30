package kubernetes

import (
	"testing"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"gotest.tools/v3/assert"
)

func TestEC2ClusterNameFromTags(t *testing.T) {
	t.Run("extracts cluster name", func(t *testing.T) {
		clusterName, err := ec2ClusterNameFromTags([]ec2types.Tag{
			{Key: stringPtr("Name"), Value: stringPtr("worker-1")},
			{Key: stringPtr("kubernetes.io/cluster/prod-cluster"), Value: stringPtr("owned")},
		})

		assert.NilError(t, err)
		assert.Equal(t, clusterName, "prod-cluster")
	})

	t.Run("ignores nil and unrelated tags", func(t *testing.T) {
		_, err := ec2ClusterNameFromTags([]ec2types.Tag{
			{Key: nil, Value: stringPtr("owned")},
			{Key: stringPtr("Name"), Value: stringPtr("worker-1")},
		})

		assert.ErrorContains(t, err, "unable to parse cluster name")
	})

	t.Run("rejects malformed cluster tag", func(t *testing.T) {
		_, err := ec2ClusterNameFromTags([]ec2types.Tag{
			{Key: stringPtr("kubernetes.io/cluster/"), Value: stringPtr("owned")},
		})

		assert.ErrorContains(t, err, "unable to parse cluster name")
	})
}

func stringPtr(value string) *string {
	return &value
}
