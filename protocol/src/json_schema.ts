import type { z } from 'zod';

export type CanonicalClosedJSONSchemaMode = 'structured_output' | 'strict_object';

type JSONSchemaDocument = Record<string, unknown>;

const canonicalClosedJSONSchemaModes = new WeakMap<object, CanonicalClosedJSONSchemaMode>();

export function registerCanonicalClosedJSONSchema<Schema extends z.ZodType>(
  schema: Schema,
  mode: CanonicalClosedJSONSchemaMode,
): Schema {
  canonicalClosedJSONSchemaModes.set(schema, mode);
  return schema;
}

export function createCanonicalJSONSchemaGenerationOverride() {
  let anchorIndex = 0;
  return ({ zodSchema, jsonSchema }: {
    zodSchema: z.core.$ZodTypes;
    jsonSchema: z.core.JSONSchema.BaseSchema;
    path: (string | number)[];
  }): void => {
    const mode = canonicalClosedJSONSchemaModes.get(zodSchema);
    if (mode === undefined) return;
    const anchorName = `canonicalClosedJSONSchemaValue${anchorIndex}`;
    anchorIndex += 1;
    replaceJSONSchema(jsonSchema, createCanonicalClosedJSONSchema(mode, anchorName));
  };
}

function createCanonicalClosedJSONSchema(mode: CanonicalClosedJSONSchemaMode, anchorName: string): JSONSchemaDocument {
  const closedValueSchema = createClosedValueSchema(mode, anchorName);
  if (mode === 'structured_output') return closedValueSchema;
  return {
    allOf: [
      closedValueSchema,
      {
        type: 'object',
        required: ['type', 'additionalProperties'],
        properties: {
          type: { const: 'object' },
          additionalProperties: createClosedObjectAdditionalPropertiesSchema(mode),
        },
      },
    ],
  };
}

function createClosedValueSchema(mode: CanonicalClosedJSONSchemaMode, anchorName: string): JSONSchemaDocument {
  return {
    $dynamicAnchor: anchorName,
    anyOf: [
      { type: 'string' },
      { type: 'number' },
      { type: 'boolean' },
      { type: 'null' },
      {
        type: 'array',
        items: { $dynamicRef: `#${anchorName}` },
      },
      {
        type: 'object',
        allOf: [createClosedObjectRule(mode)],
        additionalProperties: { $dynamicRef: `#${anchorName}` },
      },
    ],
  };
}

function createClosedObjectRule(mode: CanonicalClosedJSONSchemaMode): JSONSchemaDocument {
  return {
    if: {
      required: ['type'],
      properties: {
        type: createClosedObjectTypeCondition(mode),
      },
    },
    then: {
      required: ['additionalProperties'],
      properties: {
        additionalProperties: createClosedObjectAdditionalPropertiesSchema(mode),
      },
    },
  };
}

function createClosedObjectTypeCondition(mode: CanonicalClosedJSONSchemaMode): JSONSchemaDocument {
  if (mode === 'strict_object') return { const: 'object' };
  return {
    anyOf: [
      { const: 'object' },
      { type: 'array', contains: { const: 'object' } },
    ],
  };
}

function createClosedObjectAdditionalPropertiesSchema(mode: CanonicalClosedJSONSchemaMode): JSONSchemaDocument {
  if (mode === 'structured_output') return { const: false };
  return {
    anyOf: [
      { const: false },
      { type: 'object' },
    ],
  };
}

function replaceJSONSchema(target: JSONSchemaDocument, replacement: JSONSchemaDocument): void {
  for (const key of Object.keys(target)) delete target[key];
  Object.assign(target, replacement);
}
