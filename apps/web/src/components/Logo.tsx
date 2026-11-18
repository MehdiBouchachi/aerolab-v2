// AeroLab V2 identity artwork (public/branding, from the brand assets package). Both colour variants
// are rendered; brand.css shows the one matching the active light/dark theme.

/** Mark + AEROLAB wordmark for the sidebar and the phone header. */
export function BrandLockup() {
  return (
    <div className="brand">
      <img className="brand-mark logo-on-light" src="/branding/aerolab-mark-on-light.svg" alt="" width={30} height={30} />
      <img className="brand-mark logo-on-dark" src="/branding/aerolab-mark-on-dark.svg" alt="" width={30} height={30} />
      <span className="brand-word">AeroLab<small aria-hidden="true">V2</small></span>
    </div>
  )
}

/** Full horizontal logo (A + leaf, AEROLAB V2, "Smart aeroponics systems") for the sign-in pages.
 *  Decorative: the surrounding heading carries the accessible name. */
export function FullLogo() {
  return (
    <>
      <img className="login-logo logo-on-light" src="/branding/aerolab-logo-on-light.svg" alt="" width={290} height={76} />
      <img className="login-logo logo-on-dark" src="/branding/aerolab-logo-on-dark.svg" alt="" width={290} height={76} />
    </>
  )
}
