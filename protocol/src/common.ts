import { z } from 'zod';

export enum ExecutionMode {
  Device = 'device',
  Companion = 'companion',
  Remote = 'remote',
  Auto = 'auto',
}

export const jsonValueSchema = z.json();

export const nonNegativeIntegerSchema = z.number().int().nonnegative();

export const resourceScopeSchema = z.looseObject({
  kind: z.string().optional(),
  value: z.string().optional(),
});

export type JsonValue = z.infer<typeof jsonValueSchema>;
export type ResourceScope = z.infer<typeof resourceScopeSchema>;
