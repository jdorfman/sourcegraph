# Sourcegraph Helm Chart

## Requirements

* [Helm 3 CLI](https://helm.sh/docs/intro/install/)
* Kubernetes 1.19 or greater

## Quickstart

To use the Helm chart, add Sourcegraph helm repository:
 
```sh
helm repo add sourcegraph https://sourcegraph.github.io/deploy-sourcegraph-helm/
```

Install the Sourcegraph chart using default values:

```sh
helm install sourcegraph sourcegraph/sourcegraph
```

## Configuration guide

Helm customizations can be applied using an override file. Using an override file allows customizations to persist through upgrades without needing to manage merge conflicts.

To customize configuration settings with an override file, create an empty yaml file (e.g. `override.yaml`) and configure overrides.

> WARNING: __DO NOT__ copy the [default values file](https://github.com/sourcegraph/deploy-sourcegraph-helm/blob/main/charts/sourcegraph/values.yaml) as a boilerplate for your override file. You risk having outdated values during upgrades.

Example overrides can be found in the [examples](https://github.com/sourcegraph/deploy-sourcegraph-helm/tree/main/charts/sourcegraph/examples) folder. Please take a look at our examples before providing your own configuration and consider using them as boilerplates.

Provide the override file to helm:

```sh
# Installation
helm install --values ./override.yaml sourcegraph sourcegraph/sourcegraph

# Upgrade
helm upgrade --values ./override.yaml sourcegraph sourcegraph/sourcegraph
```

## Configuration options

The Sourcegraph chart is highly customizable to support a wide-range of environment. Please review the default values from [values.yaml](https://github.com/sourcegraph/deploy-sourcegraph-helm/blob/main/charts/sourcegraph/values.yaml) and all [supported options](https://github.com/sourcegraph/deploy-sourcegraph-helm/tree/main/charts/sourcegraph#configuration-options).

## Advanced configuration

The Helm chart is new and still under active development, and we may not cover all your use cases. 

Please contact [support@sourcegraph.com](mailto://support@sourcegraph.com) or your Customer Engineer directly to discuss your specific need.

For advanced users who are looking for a temporary workaround, we __recommend__ applying [Kustomize](https://kustomize.io) on the rendered manifests from our chart. Please __do not__ maintain your own fork of our chart, this may impact our ability to support you if you run into issues.

You can learn more about how to integrate Kustomize with Helm from our [example](https://github.com/sourcegraph/deploy-sourcegraph-helm/tree/main/charts/sourcegraph/examples/kustomize-chart).

## Upgrading Sourcegraph

> A new version of Sourcegraph is released every month (with patch releases in between, released as needed). Check the [Sourcegraph blog] for release announcements.

It's important to understand [what's changed](https://github.com/sourcegraph/deploy-sourcegraph-helm/blob/main/charts/sourcegraph/CHANGELOG.md) to the Helm chart prior upgrading the chart. If you're upgrading to a newer Sourcegraph release version, make sure you review [what's changed](https://github.com/sourcegraph/sourcegraph/blob/main/CHANGELOG.md) to Sourcegraph as well.

### Steps

> Sourcegraph Helm chart version is a separate version number from Sourcegraph release version number.

You shouod __ONLY__ upgrade one minor version at a time, first gather your current installed version. Notes the `APP VERSION` column, it indicates the current installed Sourcegraph release is `3.37.0`.

```sh
$ helm list
NAME       	NAMESPACE  	REVISION	UPDATED                             	STATUS  	CHART            	APP VERSION
sourcegraph	sourcegraph	1       	2022-03-17 17:11:06.811771 -0700 PDT	deployed	sourcegraph-0.5.0	3.37.0
```

Next, list all chart versions and choose the correct one to install. Given our current installed release is `3.37.0`, we should upgrade to `3.38.0` first (chart version `0.6.0`). Then follow the same process to upgrade to `3.39.0` after upgrading to `3.38.0`.

```sh
$ helm search repo -l sourcegraph/sourcegraph
NAME                            	CHART VERSION	APP VERSION	DESCRIPTION
sourcegraph/sourcegraph         	0.8.0        	3.39.0     	Chart for installing Sourcegraph
sourcegraph/sourcegraph         	0.6.0        	3.38.0     	Chart for installing Sourcegraph
sourcegraph/sourcegraph         	0.5.0        	3.37.0     	Chart for installing Sourcegraph
```

Next, preview the change.

> It's important to explictly reference a specific chart version with `--version` flag to avoid unexpected upgrade.

```sh
helm diff upgrade --version 0.6.0 -f ./override.yaml sourcegraph sourcegraph/sourcegraph
```

If the output looks good to you, apply the changes

```sh
helm upgrade --wait --version 0.6.0 -f ./override.yaml sourcegraph sourcegraph/sourcegraph
```

[sourcegraph blog]: https://about.sourcegraph.com/blog?_ga=2.210669782.257217162.1647902383-688195298.1646943124
