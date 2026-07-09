import { MaterialSymbol } from "../../components/ui/Icon";
import { useMemo, useState } from "react";
import { LoadingState } from "../../components/LoadingState";
import { useI18n } from "../../i18n/I18nProvider";
import type { ConfigGraph, ConfigResource, ResourceKind } from "../../rpc/types";
import { CreateResourcePanel } from "../configGraph/CreateResourcePanel";
import { ResourceEditorDialog } from "../configGraph/ResourceEditorDialog";
import { ResourceEditorCard } from "../configGraph/ResourceEditorCard";
import { useConfigGraph } from "../configGraph/useConfigGraph";
import { modelDisplayNamesById } from "../configGraph/modelProviderIcons";
import { PageHeader, QueryErrorState } from "../shared";

export function ModelsProvidersPage() {
  const { t } = useI18n();
  const graph = useConfigGraph();
  const [editing, setEditing] = useState<{ kind: ResourceKind; id: string } | null>(null);

  const modelDisplayNames = useMemo(
    () => (graph.data ? modelDisplayNamesById(graph.data.resources) : {}),
    [graph.data]
  );

  if (graph.error) {
    return <QueryErrorState error={graph.error} />;
  }
  if (graph.isLoading || !graph.data) {
    return <LoadingState label={t("loading.providers")} />;
  }

  const resources = graph.data.resources;
  const providers = resourcesByKind(resources, "provider");
  const offers = resourcesByKind(resources, "provider_offer");
  const models = resourcesByKind(resources, "model");
  const offersByModel = groupOffersByModel(offers);
  const unmatchedOffers = offers.filter((offer) => !models.some((model) => model.id === modelIdForOffer(offer)));

  const editingResource = editing
    ? resources.find((resource) => resource.kind === editing.kind && resource.id === editing.id)
    : undefined;

  return (
    <section className="page-stack" aria-labelledby="models-providers-title">
      <PageHeader eyebrow={t("pageEyebrow.upstream")} title={t("nav.modelsProviders")}>
        {t("modelsProviders.description")}
      </PageHeader>

      <section className="resource-section" aria-labelledby="providers-heading">
        <div className="section-heading">
          <h2 id="providers-heading">{t("modelsProviders.providers", { count: providers.length })}</h2>
          <CreateResourcePanel graph={graph.data} kind="provider" />
        </div>
        <div className="resource-card-list resource-card-list--summary">
          {providers.map((provider) => (
            <ResourceEditorCard
              ariaLabel={t("modelsProviders.providerRegion", { id: provider.id })}
              key={provider.id}
              onOpenEditor={() => setEditing({ kind: "provider", id: provider.id })}
              resource={provider}
              revision={graph.data.revision}
              title={t("resource.kind.provider")}
              variant="summary"
            />
          ))}
        </div>
      </section>

      {unmatchedOffers.length > 0 ? (
        <section className="resource-section" aria-labelledby="offers-heading">
          <h2 id="offers-heading">{t("modelsProviders.unmatchedSupplies", { count: unmatchedOffers.length })}</h2>
          <div className="resource-card-list resource-card-list--summary">
            {unmatchedOffers.map((offer) => (
              <ResourceEditorCard
                key={offer.id}
                modelDisplayNames={modelDisplayNames}
                onOpenEditor={() => setEditing({ kind: "provider_offer", id: offer.id })}
                resource={offer}
                revision={graph.data.revision}
                title={t("resource.kind.offer")}
                variant="summary"
              />
            ))}
          </div>
        </section>
      ) : null}

      <section className="resource-section" aria-labelledby="models-heading">
        <div className="section-heading">
          <h2 id="models-heading">{t("modelsProviders.models", { count: models.length })}</h2>
          <CreateResourcePanel graph={graph.data} kind="model" />
        </div>
        <div className="resource-card-list resource-card-list--summary">
          {models.map((model) => (
            <ResourceEditorCard
              ariaLabel={t("modelsProviders.modelRegion", { id: model.id })}
              key={model.id}
              modelDisplayNames={modelDisplayNames}
              onOpenEditor={() => setEditing({ kind: "model", id: model.id })}
              resource={model}
              revision={graph.data.revision}
              title={t("resource.kind.model")}
              variant="summary"
            />
          ))}
        </div>
      </section>

      {editingResource ? (
        <ResourceEditorDialog
          open
          onClose={() => setEditing(null)}
          modelDisplayNames={modelDisplayNames}
          resource={editingResource}
          revision={graph.data.revision}
          title={titleForResource(editingResource.kind, t)}
        >
          {editingResource.kind === "model" ? (
            <ModelProviderBindings
              graph={graph.data}
              modelDisplayNames={modelDisplayNames}
              modelId={editingResource.id}
              offers={offersByModel.get(editingResource.id) ?? []}
            />
          ) : null}
        </ResourceEditorDialog>
      ) : null}
    </section>
  );
}

function titleForResource(kind: ResourceKind, t: ReturnType<typeof useI18n>["t"]): string {
  switch (kind) {
    case "model":
      return t("resource.kind.model");
    case "provider":
      return t("resource.kind.provider");
    case "provider_offer":
      return t("resource.kind.offer");
    default:
      return "";
  }
}

function resourcesByKind(resources: ConfigResource[], kind: ResourceKind) {
  return resources.filter((resource) => resource.kind === kind);
}
function groupOffersByModel(offers: ConfigResource[]) {
  const groups = new Map<string, ConfigResource[]>();
  for (const offer of offers) {
    const modelId = modelIdForOffer(offer);
    const group = groups.get(modelId) ?? [];
    group.push(offer);
    groups.set(modelId, group);
  }
  return groups;
}

function modelIdForOffer(offer: ConfigResource) {
  const valueModel = offer.value.model;
  if (typeof valueModel === "string" && valueModel.trim()) {
    return valueModel;
  }
  const slash = offer.id.indexOf("/");
  return slash >= 0 ? offer.id.slice(slash + 1) : "";
}

function ModelProviderBindings({
  graph,
  modelDisplayNames,
  modelId,
  offers
}: {
  graph: ConfigGraph;
  modelDisplayNames: Record<string, string>;
  modelId: string;
  offers: ConfigResource[];
}) {
  const { t } = useI18n();
  const headingId = `model-${modelId}-providers-heading`;
  const bodyId = `model-${modelId}-providers-body`.replace(/[^a-zA-Z0-9_-]/g, "-");
  const offersLabel = t("modelsProviders.modelProviders", { count: offers.length });
  return (
    <section
      className="resource-field-group resource-field-group--advanced model-provider-bindings"
      aria-labelledby={headingId}
      aria-label={offersLabel}
    >
      <div className="resource-field-group__header">
        <h4 id={headingId}>
          <MaterialSymbol className="material-symbol">cloud_sync</MaterialSymbol>
          {offersLabel}
        </h4>
        <div className="resource-field-group__header-actions">
          <CreateResourcePanel graph={graph} kind="provider_offer" modelId={modelId} />
        </div>
      </div>
      <div className="resource-card-list resource-card-list--compact resource-field-group__body" id={bodyId}>
        {offers.map((offer) => (
          <ResourceEditorCard
            key={offer.id}
            modelDisplayNames={modelDisplayNames}
            resource={offer}
            revision={graph.revision}
            title={t("resource.kind.offer")}
          />
        ))}
      </div>
    </section>
  );
}
