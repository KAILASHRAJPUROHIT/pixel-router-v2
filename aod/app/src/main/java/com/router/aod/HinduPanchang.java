package com.router.aod;

import java.util.Calendar;
import java.util.TimeZone;

/**
 * Computes today's Panchang (Tithi, Paksha, Nakshatra) entirely offline
 * from the current date/time - no network dependency, unlike the router
 * stats which come from the local dashboard poll.
 *
 * Uses low-precision solar/lunar longitude formulas (Meeus, "Astronomical
 * Algorithms", truncated series - accurate to roughly a degree). That's
 * plenty for Tithi/Nakshatra, which only change at fixed longitude
 * thresholds; the one edge case is the exact hour a Tithi/Nakshatra
 * turns over, where this can be off by up to an hour or so.
 */
public final class HinduPanchang {

    private static final String[] TITHI_NAMES = {
            "Pratipada", "Dwitiya", "Tritiya", "Chaturthi", "Panchami",
            "Shashthi", "Saptami", "Ashtami", "Navami", "Dashami",
            "Ekadashi", "Dwadashi", "Trayodashi", "Chaturdashi"
    };

    private static final String[] NAKSHATRA_NAMES = {
            "Ashwini", "Bharani", "Krittika", "Rohini", "Mrigashira", "Ardra",
            "Punarvasu", "Pushya", "Ashlesha", "Magha", "Purva Phalguni", "Uttara Phalguni",
            "Hasta", "Chitra", "Swati", "Vishakha", "Anuradha", "Jyeshtha",
            "Mula", "Purva Ashadha", "Uttara Ashadha", "Shravana", "Dhanishta", "Shatabhisha",
            "Purva Bhadrapada", "Uttara Bhadrapada", "Revati"
    };

    public static final class Result {
        public final String tithi;
        public final String paksha;
        public final String nakshatra;

        Result(String tithi, String paksha, String nakshatra) {
            this.tithi = tithi;
            this.paksha = paksha;
            this.nakshatra = nakshatra;
        }
    }

    private HinduPanchang() {
    }

    public static Result compute(Calendar now) {
        double jd = julianDay(now);
        double t = (jd - 2451545.0) / 36525.0;

        double sunLon = sunEclipticLongitude(t);
        double moonLon = moonEclipticLongitude(t);

        double tithiAngle = norm360(moonLon - sunLon);
        int tithiIndex = (int) Math.floor(tithiAngle / 12.0); // 0..29
        boolean shuklaPaksha = tithiIndex < 15;
        int tithiInPaksha = shuklaPaksha ? tithiIndex : tithiIndex - 15; // 0..14
        String tithiName = tithiInPaksha == 14
                ? (shuklaPaksha ? "Purnima" : "Amavasya")
                : TITHI_NAMES[tithiInPaksha];

        int nakshatraIndex = (int) Math.floor(norm360(moonLon) / (360.0 / 27.0));
        nakshatraIndex = Math.max(0, Math.min(26, nakshatraIndex));

        return new Result(tithiName, shuklaPaksha ? "Shukla" : "Krishna", NAKSHATRA_NAMES[nakshatraIndex]);
    }

    private static double julianDay(Calendar cal) {
        Calendar utc = (Calendar) cal.clone();
        utc.setTimeZone(TimeZone.getTimeZone("UTC"));
        int year = utc.get(Calendar.YEAR);
        int month = utc.get(Calendar.MONTH) + 1;
        double day = utc.get(Calendar.DAY_OF_MONTH)
                + utc.get(Calendar.HOUR_OF_DAY) / 24.0
                + utc.get(Calendar.MINUTE) / 1440.0
                + utc.get(Calendar.SECOND) / 86400.0;
        if (month <= 2) {
            year -= 1;
            month += 12;
        }
        int a = year / 100;
        int b = 2 - a + a / 4;
        return Math.floor(365.25 * (year + 4716)) + Math.floor(30.6001 * (month + 1)) + day + b - 1524.5;
    }

    /** Low-precision Sun ecliptic longitude (Meeus ch. 25, truncated). */
    private static double sunEclipticLongitude(double t) {
        double l0 = norm360(280.46646 + 36000.76983 * t + 0.0003032 * t * t);
        double m = norm360(357.52911 + 35999.05029 * t - 0.0001537 * t * t);
        double mRad = Math.toRadians(m);
        double c = (1.914602 - 0.004817 * t - 0.000014 * t * t) * Math.sin(mRad)
                + (0.019993 - 0.000101 * t) * Math.sin(2 * mRad)
                + 0.000289 * Math.sin(3 * mRad);
        return norm360(l0 + c);
    }

    /** Low-precision Moon ecliptic longitude (Meeus ch. 47, truncated to main terms). */
    private static double moonEclipticLongitude(double t) {
        double lp = norm360(218.3164477 + 481267.88123421 * t);
        double d = norm360(297.8501921 + 445267.1114034 * t);
        double m = norm360(357.5291092 + 35999.0502909 * t);
        double mp = norm360(134.9633964 + 477198.8675055 * t);

        double dRad = Math.toRadians(d);
        double mRad = Math.toRadians(m);
        double mpRad = Math.toRadians(mp);

        double dLon = 6.288774 * Math.sin(mpRad)
                + 1.274027 * Math.sin(2 * dRad - mpRad)
                + 0.658314 * Math.sin(2 * dRad)
                + 0.213618 * Math.sin(2 * mpRad)
                - 0.185116 * Math.sin(mRad)
                - 0.114332 * Math.sin(2 * (dRad - mpRad) + mpRad - dRad + dRad)
                + 0.058793 * Math.sin(2 * (dRad - mpRad))
                + 0.057066 * Math.sin(2 * dRad - mRad - mpRad)
                + 0.053322 * Math.sin(2 * dRad + mpRad)
                + 0.045758 * Math.sin(2 * dRad - mRad);

        return norm360(lp + dLon);
    }

    private static double norm360(double deg) {
        double r = deg % 360.0;
        return r < 0 ? r + 360.0 : r;
    }
}
