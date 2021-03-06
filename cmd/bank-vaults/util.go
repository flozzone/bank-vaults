// Copyright © 2020 Banzai Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"emperror.dev/errors"
	"github.com/spf13/viper"

	"github.com/banzaicloud/bank-vaults/pkg/kv"
	"github.com/banzaicloud/bank-vaults/pkg/kv/alibabakms"
	"github.com/banzaicloud/bank-vaults/pkg/kv/alibabaoss"
	"github.com/banzaicloud/bank-vaults/pkg/kv/awskms"
	"github.com/banzaicloud/bank-vaults/pkg/kv/azurekv"
	"github.com/banzaicloud/bank-vaults/pkg/kv/dev"
	"github.com/banzaicloud/bank-vaults/pkg/kv/file"
	"github.com/banzaicloud/bank-vaults/pkg/kv/gckms"
	"github.com/banzaicloud/bank-vaults/pkg/kv/gcs"
	"github.com/banzaicloud/bank-vaults/pkg/kv/hsm"
	"github.com/banzaicloud/bank-vaults/pkg/kv/k8s"
	"github.com/banzaicloud/bank-vaults/pkg/kv/multi"
	"github.com/banzaicloud/bank-vaults/pkg/kv/s3"
	kvvault "github.com/banzaicloud/bank-vaults/pkg/kv/vault"
	"github.com/banzaicloud/bank-vaults/pkg/sdk/vault"
)

// TODO review this function's returned error
// nolint: unparam
func vaultConfigForConfig(_ *viper.Viper) (vault.Config, error) {
	return vault.Config{
		SecretShares:    appConfig.GetInt(cfgSecretShares),
		SecretThreshold: appConfig.GetInt(cfgSecretThreshold),

		InitRootToken:  appConfig.GetString(cfgInitRootToken),
		StoreRootToken: appConfig.GetBool(cfgStoreRootToken),

		PreFlightChecks: appConfig.GetBool(cfgPreFlightChecks),
	}, nil
}

func kvStoreForConfig(cfg *viper.Viper) (kv.Service, error) {
	switch mode := cfg.GetString(cfgMode); mode {
	case cfgModeValueGoogleCloudKMSGCS:
		gcs, err := gcs.New(
			cfg.GetString(cfgGoogleCloudStorageBucket),
			cfg.GetString(cfgGoogleCloudStoragePrefix),
		)
		if err != nil {
			return nil, errors.Wrap(err, "error creating google cloud storage kv store")
		}

		kms, err := gckms.New(gcs,
			cfg.GetString(cfgGoogleCloudKMSProject),
			cfg.GetString(cfgGoogleCloudKMSLocation),
			cfg.GetString(cfgGoogleCloudKMSKeyRing),
			cfg.GetString(cfgGoogleCloudKMSCryptoKey),
		)
		if err != nil {
			return nil, errors.Wrap(err, "error creating google cloud kms kv store")
		}

		return kms, nil

	case cfgModeValueAWSKMS3:

		s3Regions := cfg.GetStringSlice(cfgAWSS3Region)
		s3Buckets := cfg.GetStringSlice(cfgAWSS3Bucket)
		s3Prefix := cfg.GetString(cfgAWSS3Prefix)

		if len(s3Regions) != len(s3Buckets) {
			return nil, errors.Errorf("specify the same number of regions and buckets for AWS S3 kv store [%d != %d]", len(s3Regions), len(s3Buckets))
		}

		var s3Services []kv.Service

		for i := range s3Regions {
			s3Service, err := s3.New(
				s3Regions[i],
				s3Buckets[i],
				s3Prefix,
			)
			if err != nil {
				return nil, errors.Wrap(err, "error creating AWS S3 kv store")
			}

			s3Services = append(s3Services, s3Service)
		}

		kmsRegions := cfg.GetStringSlice(cfgAWSKMSRegion)
		kmsKeyIDs := cfg.GetStringSlice(cfgAWSKMSKeyID)

		if len(kmsRegions) != len(kmsKeyIDs) {
			return nil, errors.Errorf("specify the same number of regions and key IDs for AWS KMS kv store")
		}

		if len(kmsRegions) != len(s3Regions) {
			return nil, errors.Errorf("specify the same number of S3 buckets and KMS keys for AWS kv store")
		}

		var kmsServices []kv.Service

		for i := range kmsRegions {
			kmsService, err := awskms.New(s3Services[i], kmsRegions[i], kmsKeyIDs[i])
			if err != nil {
				return nil, errors.Wrap(err, "error creating AWS KMS kv store")
			}

			kmsServices = append(kmsServices, kmsService)
		}

		return multi.New(kmsServices), nil

	case cfgModeValueAzureKeyVault:
		akv, err := azurekv.New(cfg.GetString(cfgAzureKeyVaultName))
		if err != nil {
			return nil, errors.Wrap(err, "error creating Azure Key Vault kv store")
		}

		return akv, nil

	case cfgModeValueAlibabaKMSOSS:
		accessKeyID := cfg.GetString(cfgAlibabaAccessKeyID)
		accessKeySecret := cfg.GetString(cfgAlibabaAccessKeySecret)

		if accessKeyID == "" || accessKeySecret == "" {
			return nil, errors.Errorf("Alibaba accessKeyID or accessKeySecret can't be empty")
		}

		bucket := cfg.GetString(cfgAlibabaOSSBucket)

		if bucket == "" {
			return nil, errors.Errorf("Alibaba OSS bucket should be specified")
		}

		oss, err := alibabaoss.New(
			cfg.GetString(cfgAlibabaOSSEndpoint),
			accessKeyID,
			accessKeySecret,
			bucket,
			cfg.GetString(cfgAlibabaOSSPrefix),
		)
		if err != nil {
			return nil, errors.Wrap(err, "error creating Alibaba OSS kv store")
		}

		kms, err := alibabakms.New(
			cfg.GetString(cfgAlibabaKMSRegion),
			accessKeyID,
			accessKeySecret,
			cfg.GetString(cfgAlibabaKMSKeyID),
			oss)
		if err != nil {
			return nil, errors.Wrap(err, "error creating Alibaba KMS kv store")
		}

		return kms, nil

	case cfgModeValueVault:
		vault, err := kvvault.New(
			cfg.GetString(cfgVaultAddress),
			cfg.GetString(cfgVaultUnsealKeysPath),
			cfg.GetString(cfgVaultRole),
			cfg.GetString(cfgVaultAuthPath),
			cfg.GetString(cfgVaultTokenPath),
			cfg.GetString(cfgVaultToken))
		if err != nil {
			return nil, errors.Wrap(err, "error creating Vault kv store")
		}

		return vault, nil

	case cfgModeValueK8S:
		k8s, err := k8s.New(
			cfg.GetString(cfgK8SNamespace),
			cfg.GetString(cfgK8SSecret),
			k8sSecretLabels,
		)
		if err != nil {
			return nil, errors.Wrap(err, "error creating K8S Secret kv store")
		}

		return k8s, nil

	// BANK_VAULTS_HSM_PIN=banzai bank-vaults unseal --init --mode hsm-k8s --k8s-secret-name hsm --k8s-secret-namespace default --hsm-slot-id 0
	case cfgModeValueHSMK8S:
		k8s, err := k8s.New(
			cfg.GetString(cfgK8SNamespace),
			cfg.GetString(cfgK8SSecret),
			k8sSecretLabels,
		)
		if err != nil {
			return nil, errors.Wrap(err, "error creating K8S Secret with with kv store")
		}

		config := hsm.Config{
			ModulePath: cfg.GetString(cfgHSMModulePath),
			SlotID:     cfg.GetUint(cfgHSMSlotID),
			TokenLabel: cfg.GetString(cfgHSMTokenLabel),
			Pin:        cfg.GetString(cfgHSMPin),
			KeyLabel:   cfg.GetString(cfgHSMKeyLabel),
		}

		hsm, err := hsm.New(config, k8s)
		if err != nil {
			return nil, errors.Wrap(err, "error creating HSM kv store")
		}

		return hsm, nil

	// BANK_VAULTS_HSM_PIN=banzai bank-vaults unseal --init --mode hsm --hsm-slot-id 0 --hsm-module-path /usr/local/lib/opensc-pkcs11.so
	case cfgModeValueHSM:
		config := hsm.Config{
			ModulePath: cfg.GetString(cfgHSMModulePath),
			SlotID:     cfg.GetUint(cfgHSMSlotID),
			TokenLabel: cfg.GetString(cfgHSMTokenLabel),
			Pin:        cfg.GetString(cfgHSMPin),
			KeyLabel:   cfg.GetString(cfgHSMKeyLabel),
		}

		hsm, err := hsm.New(config, nil)
		if err != nil {
			return nil, errors.Wrap(err, "error creating HSM kv store")
		}

		return hsm, nil

	case cfgModeValueDev:
		dev, err := dev.New()
		if err != nil {
			return nil, errors.Wrap(err, "error creating Dev Secret kv store")
		}

		return dev, nil

	case cfgModeValueFile:
		file, err := file.New(cfg.GetString(cfgFilePath))
		if err != nil {
			return nil, errors.Wrap(err, "error creating File kv store")
		}

		return file, nil

	default:
		return nil, errors.Errorf("unsupported backend mode: '%s'", cfg.GetString(cfgMode))
	}
}
