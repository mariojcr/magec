const METADATA_REGEX = /<!--MAGEC_META:.*?:MAGEC_META-->\n?/gs

export function stripMetadata(text) {
  if (!text) return text
  return text.replace(METADATA_REGEX, '').trimStart()
}
